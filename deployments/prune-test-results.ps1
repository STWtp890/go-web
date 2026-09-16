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

# Documents under docs/ must cite the immutable snapshots in docs/reports/evidence/,
# not this rolling directory: pruning older runs is exactly how 23 phase-report
# evidence links once rotted. This guard is a safety net for any reference that still
# slips in, so a referenced artifact is never deleted.
$referencedNames = New-Object System.Collections.Generic.HashSet[string]
$trackedDocuments = & git -C $repositoryRoot -c core.quotepath=false ls-files -- '*.md'
if ($LASTEXITCODE -ne 0) {
    throw "git ls-files failed with exit code $LASTEXITCODE (is $repositoryRoot a git work tree?)"
}
foreach ($document in $trackedDocuments) {
    if ([string]::IsNullOrWhiteSpace($document)) { continue }
    $documentPath = Join-Path $repositoryRoot ($document -replace '/', [System.IO.Path]::DirectorySeparatorChar)
    if (-not (Test-Path -LiteralPath $documentPath -PathType Leaf)) { continue }
    foreach ($match in [regex]::Matches((Get-Content -Raw -LiteralPath $documentPath), 'test-results/([A-Za-z0-9._-]+)')) {
        $null = $referencedNames.Add($match.Groups[1].Value)
    }
}

# Group the actual file objects. Never rebuild a file name from the regex groups:
# the older generation uses '-' before the date, so a rebuilt '<family>_<stamp>'
# would not exist and the deletion would be skipped silently.
$filesByFamily = @{}
$unrecognised = New-Object System.Collections.Generic.List[string]

foreach ($file in (Get-ChildItem -LiteralPath $resultsDirectory -File)) {
    if ($file.Name -notmatch $pattern) {
        $unrecognised.Add($file.Name)
        continue
    }
    $family = $Matches['family']
    $stamp = $Matches['stamp']

    if (-not $filesByFamily.ContainsKey($family)) {
        $filesByFamily[$family] = @{}
    }
    if (-not $filesByFamily[$family].ContainsKey($stamp)) {
        $filesByFamily[$family][$stamp] = New-Object System.Collections.Generic.List[System.IO.FileInfo]
    }
    $null = $filesByFamily[$family][$stamp].Add($file)
}

$removed = 0
$kept = 0
$referenced = 0
foreach ($family in ($filesByFamily.Keys | Sort-Object)) {
    $stamps = @($filesByFamily[$family].Keys) | Sort-Object -Descending
    $keepStamps = @($stamps | Select-Object -First $KeepRuns)
    $staleStamps = @($stamps | Select-Object -Skip $KeepRuns)

    Write-Host ("{0,-32} runs={1} keep={2} stale={3}" -f `
        $family, $stamps.Count, $keepStamps.Count, $staleStamps.Count)

    foreach ($stamp in $keepStamps) {
        $kept += $filesByFamily[$family][$stamp].Count
    }

    foreach ($stamp in $staleStamps) {
        foreach ($file in $filesByFamily[$family][$stamp]) {
            if ($referencedNames.Contains($file.Name)) {
                Write-Host "  kept (referenced by a tracked document): $($file.FullName)"
                $referenced++
                continue
            }
            if ($DryRun) {
                Write-Host "  would remove: $($file.FullName)"
            }
            else {
                Remove-Item -LiteralPath $file.FullName -Force
                Write-Host "  removed: $($file.FullName)"
            }
            $removed++
        }
    }
}

if ($unrecognised.Count -gt 0) {
    Write-Host ""
    Write-Host "WARNING: unrecognised report file names (skipped):"
    foreach ($name in $unrecognised) {
        Write-Host "  $name"
    }
}

Write-Host ""
Write-Host ("PRUNE done: families={0} keptFiles={1} removedFiles={2} keepRuns={3} skipped={4} referencedHeld={5} dryRun={6}" -f `
    $filesByFamily.Count, $kept, $removed, $KeepRuns, $unrecognised.Count, $referenced, [bool]$DryRun)
