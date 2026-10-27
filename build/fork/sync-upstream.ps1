$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Push-Location $repoRoot
try {
    $branch = git branch --show-current
    if ($LASTEXITCODE -ne 0 -or -not $branch) { throw 'Check out a local branch first' }
    $changes = git status --porcelain
    if ($LASTEXITCODE -ne 0 -or $changes) { throw 'Commit or stash local changes before merging upstream' }
    $source = git remote get-url upstream
    if ($LASTEXITCODE -ne 0 -or $source -notmatch '^(https://github\.com/|git@github\.com:)yetone/magpie(\.git)?$') {
        throw 'upstream must point to yetone/magpie'
    }
    git fetch upstream main
    if ($LASTEXITCODE -ne 0) { throw 'Fetching upstream failed' }
    git merge --no-edit upstream/main
    if ($LASTEXITCODE -ne 0) { throw 'Resolve the reported conflicts, or use git merge --abort; nothing was pushed' }
    Write-Output "Merged upstream/main into $branch. Run the checks in FORK.md before pushing to origin."
} finally {
    Pop-Location
}
