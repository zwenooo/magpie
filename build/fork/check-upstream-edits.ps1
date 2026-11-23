# Fails when the upstream files this branch changes or deletes differ from
# build/fork/upstream-edits.txt: a changed file nobody listed, or a listed
# file that is upstream's again. Run it after committing a fork change or
# an upstream merge; it compares HEAD, not the working tree.
param(
    # the upstream revision whose merge base with HEAD is the fork's base
    [string]$Upstream = 'upstream/main'
)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
Push-Location $repoRoot
try {
    $base = git merge-base HEAD $Upstream
    if ($LASTEXITCODE -ne 0 -or -not $base) {
        throw "No merge base between HEAD and $Upstream; fetch upstream first"
    }
    $changed = @(git -c core.quotepath=off diff --no-renames --name-only --diff-filter=MD $base HEAD)
    if ($LASTEXITCODE -ne 0) { throw 'git diff failed' }
    $listPath = Join-Path $PSScriptRoot 'upstream-edits.txt'
    $listed = @(Get-Content -LiteralPath $listPath | ForEach-Object { ($_ -replace '#.*$', '').Trim() } | Where-Object { $_ })

    $changedSet = [System.Collections.Generic.HashSet[string]]::new([string[]]$changed, [System.StringComparer]::Ordinal)
    $listedSet = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    $twice = @($listed | Where-Object { -not $listedSet.Add($_) })
    $unlisted = @($changed | Where-Object { -not $listedSet.Contains($_) })
    $stale = @($listedSet | Where-Object { -not $changedSet.Contains($_) })

    $short = git rev-parse --short $base
    if ($unlisted.Count -eq 0 -and $stale.Count -eq 0 -and $twice.Count -eq 0) {
        Write-Output "$($changed.Count) upstream files changed since $short, each listed in build/fork/upstream-edits.txt."
        exit 0
    }
    if ($unlisted.Count) {
        Write-Output "Changed from upstream $short but not in build/fork/upstream-edits.txt (add each under the group it serves, and say why in FORK.md if it is a fix):"
        $unlisted | ForEach-Object { Write-Output "  $_" }
    }
    if ($stale.Count) {
        Write-Output "Listed but the same as upstream $short now (take them out of the list, and out of FORK.md if named there):"
        $stale | ForEach-Object { Write-Output "  $_" }
    }
    if ($twice.Count) {
        Write-Output 'Listed more than once:'
        $twice | Sort-Object -Unique | ForEach-Object { Write-Output "  $_" }
    }
    exit 1
} finally {
    Pop-Location
}
