[CmdletBinding()]
param(
    [string]$ComposeFile = 'docker-compose.yaml',
    [string]$Service = 'postgres',
    [string]$Database = 'gin_demo',
    [string]$User = 'postgres'
)

# One-shot gate: prove in the running development database that each source-owned
# service can write its own schema, that document-search holds only a read grant
# on the document service Outbox, and that no service can write another service's
# business tables.
#
# Keep this file ASCII-only. Windows PowerShell 5.1 parses a BOM-less .ps1 as
# ANSI, and a multi-byte comment can swallow the following newline.

$ErrorActionPreference = 'Stop'

# This script lives in deployments/postgresql; the probe is beside it in
# sql/service, and the Compose file is two levels up at the repository root.
# Resolving against the wrong parent would silently look in deployments/sql.
$probeDirectory = (Resolve-Path $PSScriptRoot).Path
$probePath = Join-Path $probeDirectory 'sql/service/verify_service_isolation.sql'
$containerPath = '/sql/service/verify_service_isolation.sql'
$composeDirectory = (Resolve-Path (Join-Path $probeDirectory '../..')).Path
$composePath = Join-Path $composeDirectory $ComposeFile

if (-not (Test-Path -LiteralPath $probePath -PathType Leaf)) {
    throw "isolation probe is missing: $probePath"
}
if (-not (Test-Path -LiteralPath $composePath -PathType Leaf)) {
    throw "compose file is missing: $composePath"
}

# Native stderr is captured explicitly: `docker compose exec` writes the server's
# notices to stderr, and a plain call would let PowerShell treat that as a
# terminating error and lose the real exit code.
Write-Host "==> running $probePath against $Service/$Database"

$startInfo = New-Object System.Diagnostics.ProcessStartInfo
$startInfo.FileName = 'docker'
$startInfo.Arguments = 'compose -f "{0}" exec -T {1} psql -X -v ON_ERROR_STOP=1 -U {2} -d {3} -f {4}' -f `
    $composePath, $Service, $User, $Database, $containerPath
$startInfo.WorkingDirectory = $composeDirectory
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true

$process = New-Object System.Diagnostics.Process
$process.StartInfo = $startInfo
if (-not $process.Start()) {
    throw 'could not start docker compose'
}
$standardOutput = $process.StandardOutput.ReadToEnd()
$standardError = $process.StandardError.ReadToEnd()
$process.WaitForExit()
$exitCode = $process.ExitCode

foreach ($line in @($standardOutput -split "`r?`n") + @($standardError -split "`r?`n")) {
    if (-not [string]::IsNullOrWhiteSpace($line)) { Write-Host $line }
}

if ($exitCode -ne 0) {
    throw "service isolation probe failed with exit code $exitCode"
}
if ((($standardOutput + $standardError) -notmatch 'SERVICE_ISOLATION_OK')) {
    throw 'service isolation probe did not report SERVICE_ISOLATION_OK'
}

Write-Host "`nSERVICE_ISOLATION=PASS"
