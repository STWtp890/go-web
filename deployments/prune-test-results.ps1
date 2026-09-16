[CmdletBinding()]
param(
    [ValidateRange(1, 100)]
    [int]$KeepRuns = 3,
    [switch]$DryRun
)

# Keeps deployments/test-results bounded. A full verification run appends six files
# (three report families x json + md); only the most recent runs stay in git.
#
# Keep this file ASCII-only. Windows PowerShell 5.1 parses a BOM-less .ps1 as ANSI,
# and a multi-byte comment can swallow the following newline, silently breaking code.
# CI's hygiene job asserts that every .ps1 stays ASCII-only.

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$resultsDirectory = Join-Path -Path $repositoryRoot -ChildPath 'deployments/test-results'

if (-not (Test-Path -LiteralPath $resultsDirectory -PathType Container)) {
    throw "results directory not found: $resultsDirectory"
}

# Accepts both naming generations:
#   current: <family>_<YYYYMMDD>_<HHMMSS>.<ext>
#   older:   <family>-<YYYYMMDD>_<HHMMSS>.<ext>
$pattern = '^(?<family>.+?)[_-](?<stamp>\d{8}_\d{6})\.(?<ext>json|md)$'

$stampsByFamily = @{}
$unrecognised = New-Object System.Collections.Generic.List[string]

foreach ($file in (Get-ChildItem -LiteralPath $resultsDirectory -File)) {
    if ($file.Name -notmatch $pattern) {
        $unrecognised.Add($file.Name)
        continue
    }
    $family = $Matches['family']
    $stamp = $Matches['stamp']
    if (-not $stampsByFamily.ContainsKey($family)) {
        $stampsByFamily[$family] = New-Object System.Collections.Generic.HashSet[string]
    }
    $null = $stampsByFamily[$family].Add($stamp)
}

$removed = 0
$kept = 0
foreach ($family in ($stampsByFamily.Keys | Sort-Object)) {
    $ordered = @($stampsByFamily[$family]) | Sort-Object -Descending
    $stale = @($ordered | Select-Object -Skip $KeepRuns)
    Write-Host ("{0,-32} runs={1} keep={2} stale={3}" -f `
        $family, $ordered.Count, [Math]::Min($KeepRuns, $ordered.Count), $stale.Count)

    foreach ($stamp in $stale) {
        foreach ($extension in @('json', 'md')) {
            $candidate = Join-Path -Path $resultsDirectory -ChildPath ("{0}_{1}.{2}" -f $family, $stamp, $extension)
            if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
                continue
            }
            if ($DryRun) {
                Write-Host "  would remove: $candidate"
            }
            else {
                Remove-Item -LiteralPath $candidate -Force
                Write-Host "  removed: $candidate"
            }
            $removed++
        }
    }
    $kept += ([Math]::Min($KeepRuns, $ordered.Count) * 2)
}

if ($unrecognised.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING: unrecognised report file names (skipped):"
    foreach ($name in $unrecognised) {
        Write-Host "  $name"
    }
}

Write-Host ""
Write-Host ("PRUNE done: families={0} keptFiles={1} removedFiles={2} keepRuns={3} skipped={4} dryRun={5}" -f `
    $stampsByFamily.Count, $kept, $removed, $KeepRuns, $unrecognised.Count, [bool]$DryRun)
