param(
    [Parameter(Mandatory)][ValidateSet('windows', 'darwin', 'linux')][string]$TargetOS,
    [Parameter(Mandatory)][ValidateSet('amd64', 'arm64')][string]$TargetArch,
    [string]$OutputDirectory,
    [switch]$Verify
)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$source = Join-Path $repoRoot 'native/cursor-backend'
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $repoRoot 'internal/cursorbyok/payload' }
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
$targets = @{
    'windows/amd64' = 'x86_64-pc-windows-msvc'
    'windows/arm64' = 'aarch64-pc-windows-msvc'
    'darwin/amd64' = 'x86_64-apple-darwin'
    'darwin/arm64' = 'aarch64-apple-darwin'
    'linux/amd64' = 'x86_64-unknown-linux-gnu'
    'linux/arm64' = 'aarch64-unknown-linux-gnu'
}
$target = $targets["$TargetOS/$TargetArch"]
$extension = if ($TargetOS -eq 'windows') { '.exe' } else { '' }
$filename = "magpie-cursor-backend$extension"
$previousDeploymentTarget = $env:MACOSX_DEPLOYMENT_TARGET
if ($TargetOS -eq 'darwin' -and -not $previousDeploymentTarget) { $env:MACOSX_DEPLOYMENT_TARGET = '12.0' }
Push-Location $source
try {
    # rust-toolchain.toml pins the compiler; Cargo.lock pins all dependencies.
    & rustup target add $target
    if ($LASTEXITCODE -ne 0) { throw "Cannot install Rust target $target" }
    & cargo build --locked --release --manifest-path Cargo.toml --bin magpie-cursor-backend --target $target --target-dir target
    if ($LASTEXITCODE -ne 0) { throw 'Cursor backend build failed' }
    if ($Verify) {
        # Tests exercise private IPC and restoration using temporary configs.
        # Pin Rust caches before redirecting every user-config folder.
        $variables = @('HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'CARGO_HOME', 'RUSTUP_HOME')
        $previous = @{}
        foreach ($name in $variables) { $previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
        $profilePath = [Environment]::GetFolderPath('UserProfile')
        $cargoDirectory = if ($env:CARGO_HOME) { $env:CARGO_HOME } else { Join-Path $profilePath '.cargo' }
        $rustupDirectory = if ($env:RUSTUP_HOME) { $env:RUSTUP_HOME } else { Join-Path $profilePath '.rustup' }
        $testHome = Join-Path ([IO.Path]::GetTempPath()) ("magpie-cursor-tests-" + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $testHome | Out-Null
        try {
            foreach ($name in @('HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME')) {
                [Environment]::SetEnvironmentVariable($name, $testHome, 'Process')
            }
            $env:CARGO_HOME = $cargoDirectory
            $env:RUSTUP_HOME = $rustupDirectory
            & cargo test --locked --release --manifest-path Cargo.toml -p cursor-server --lib managed --target $target --target-dir target
            if ($LASTEXITCODE -ne 0) { throw 'Cursor backend isolation and restore tests failed' }
            & cargo test --locked --release --manifest-path Cargo.toml -p cursor-server --test managed_child --target $target --target-dir target
            if ($LASTEXITCODE -ne 0) { throw 'Cursor backend lifecycle test failed' }
        } finally {
            foreach ($name in $variables) { [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process') }
            $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
            $resolvedTestHome = [IO.Path]::GetFullPath($testHome)
            if (-not $resolvedTestHome.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) {
                throw 'Refusing to remove a test folder outside the temporary directory'
            }
            Remove-Item -LiteralPath $resolvedTestHome -Recurse -Force
        }
    }
    New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
    $binary = Join-Path $OutputDirectory $filename
    Copy-Item -LiteralPath "target/$target/release/$filename" -Destination $binary -Force
    $otherFilename = if ($TargetOS -eq 'windows') { 'magpie-cursor-backend' } else { 'magpie-cursor-backend.exe' }
    $otherBinary = Join-Path $OutputDirectory $otherFilename
    if (Test-Path -LiteralPath $otherBinary) { Remove-Item -LiteralPath $otherBinary -Force }
    if ($TargetOS -eq 'darwin') {
        & codesign --force --sign - $binary
        if ($LASTEXITCODE -ne 0) { throw 'Cursor backend ad-hoc signing failed' }
    }
    $revision = git -C $repoRoot rev-parse HEAD
    if ($LASTEXITCODE -ne 0) { throw 'Cannot identify the backend source commit' }
    $metadata = [ordered]@{
        schema = 1
        source = $revision.Trim()
        os = $TargetOS
        arch = $TargetArch
        target = $target
        file = $filename
        sha256 = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    $metadata | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $OutputDirectory 'backend.json') -Encoding utf8NoBOM
    Copy-Item -LiteralPath (Join-Path $repoRoot 'THIRD-PARTY-NOTICES.txt') -Destination $OutputDirectory -Force
    Write-Output $binary
} finally {
    Pop-Location
    [Environment]::SetEnvironmentVariable('MACOSX_DEPLOYMENT_TARGET', $previousDeploymentTarget, 'Process')
}
