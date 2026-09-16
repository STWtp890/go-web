[CmdletBinding()]
param(
    [string]$RepositoryRoot
)

# Verifies the maintained documentation set in two ways:
#
#   1. every relative Markdown link resolves to an existing file or directory;
#   2. no document under docs/ links into deployments/test-results, which is a
#      bounded operational directory pruned by deployments/prune-test-results.ps1.
#      Frozen phase reports cite the immutable snapshots in docs/reports/evidence/
#      instead, so their evidence never rots when a newer run replaces an old one.
#
# Absolute URLs, mailto: targets and pure anchors are skipped. Tracked Markdown
# files are enumerated through git so that vendored trees (.agents/) and build
# output (node_modules/) are never scanned.
#
# Keep this file ASCII-only. Windows PowerShell 5.1 parses a BOM-less .ps1 as ANSI,
# and a multi-byte comment can swallow the following newline, silently breaking code.
# CI's hygiene job asserts that every .ps1 stays ASCII-only.

$ErrorActionPreference = 'Stop'

if (-not $RepositoryRoot) {
    $RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
}
$RepositoryRoot = (Resolve-Path -LiteralPath $RepositoryRoot).Path

# Frozen evidence and operational results are the only directories whose contents
# are allowed to disappear; links into them would be unstable by design.
$unstablePrefix = 'deployments/test-results/'

$tracked = & git -C $RepositoryRoot -c core.quotepath=false ls-files -- '*.md'
if ($LASTEXITCODE -ne 0) {
    throw "git ls-files failed with exit code $LASTEXITCODE (is $RepositoryRoot a git work tree?)"
}

$linkPattern = '\]\(([^)]+)\)'
$broken = New-Object System.Collections.Generic.List[string]
$unstable = New-Object System.Collections.Generic.List[string]
$checked = 0

foreach ($relativePath in $tracked) {
    if ([string]::IsNullOrWhiteSpace($relativePath)) { continue }
    $fullPath = Join-Path $RepositoryRoot ($relativePath -replace '/', [System.IO.Path]::DirectorySeparatorChar)
    if (-not (Test-Path -LiteralPath $fullPath -PathType Leaf)) { continue }

    $directory = Split-Path -Parent $fullPath
    $lines = Get-Content -LiteralPath $fullPath -Encoding UTF8
    for ($index = 0; $index -lt $lines.Count; $index++) {
        foreach ($match in [regex]::Matches($lines[$index], $linkPattern)) {
            $target = $match.Groups[1].Value.Trim()
            if ($target -match '^(https?:|mailto:|tel:|#)') { continue }

            $path = ($target -split '#')[0]
            if ([string]::IsNullOrWhiteSpace($path)) { continue }

            $checked++
            $normalized = ($relativePath -replace '\\', '/')
            $resolvedRelative = $target -replace '\\', '/'
            $resolvedRelative = ($resolvedRelative -split '#')[0]

            # A docs/ document may still describe the operational directory in prose
            # or in backticks; only a real link makes the document depend on it.
            if ($normalized.StartsWith('docs/') -and $resolvedRelative -match $unstablePrefix) {
                $unstable.Add(("{0}:{1}: {2}" -f $normalized, ($index + 1), $target))
            }

            $targetPath = Join-Path $directory ($path -replace '/', [System.IO.Path]::DirectorySeparatorChar)
            if (-not (Test-Path -LiteralPath $targetPath)) {
                $broken.Add(("{0}:{1}: {2}" -f $normalized, ($index + 1), $target))
            }
        }
    }
}

foreach ($entry in $unstable) {
    Write-Host "UNSTABLE LINK $entry"
}
foreach ($entry in $broken) {
    Write-Host "BROKEN LINK   $entry"
}

Write-Host ""
Write-Host ("DOC_LINKS checked={0} documents={1} broken={2} unstable={3}" -f `
    $checked, $tracked.Count, $broken.Count, $unstable.Count)

if ($broken.Count -gt 0) {
    exit 1
}
if ($unstable.Count -gt 0) {
    Write-Host "docs/ must link frozen evidence under docs/reports/evidence/ instead."
    exit 1
}

Write-Host "DOC_LINKS=PASS"
exit 0
