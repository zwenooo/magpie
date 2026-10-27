param(
    [string]$Version,
    [switch]$NoGUI
)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Push-Location $repoRoot
try {
    if (-not $Version) {
        $revision = git rev-parse --short HEAD
        if ($LASTEXITCODE -ne 0) { throw 'Cannot read the source revision' }
        $Version = "fork-$revision"
    }
    if ($Version -notmatch '^[A-Za-z0-9._+-]+$') { throw 'Invalid version' }
    $targetOS = go env GOOS
    if ($LASTEXITCODE -ne 0) { throw 'Go is required; use the version in go.mod' }
    $targetArch = go env GOARCH
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read Go target architecture' }
    $flavor = if ($NoGUI) { 'cli' } else { 'desktop' }
    $extension = if ($targetOS -eq 'windows') { '.exe' } else { '' }
    $output = "dist/magpie-fork-$flavor-$targetOS-$targetArch$extension"
    New-Item -ItemType Directory -Force -Path 'dist' | Out-Null
    $buildArgs = @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$Version", '-o', $output)
    if ($NoGUI) { $buildArgs += @('-tags', 'nogui') }
    else { $buildArgs += @('-tags', 'production') }
    $buildArgs += '.'
    & go @buildArgs
    if ($LASTEXITCODE -ne 0) { throw 'Fork build failed' }
    Write-Output (Join-Path $repoRoot $output)
} finally {
    Pop-Location
}
