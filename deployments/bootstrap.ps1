[CmdletBinding()]
param(
    [string]$PgSearchUrl = 'https://github.com/paradedb/paradedb/releases/download/v0.25.2/postgresql-17-pg-search_0.25.2-1PARADEDB-bookworm_amd64.deb',
    [switch]$Force
)

# One-time developer bootstrap: restore the artifacts a fresh clone lacks.
# Both the JWT key pair and the pg_search .deb are deliberately gitignored, so
# `docker compose up --build` fails on a clean clone until they exist.
#
# Keep this file ASCII-only. Windows PowerShell 5.1 parses a BOM-less .ps1 as ANSI,
# and a multi-byte comment can swallow the following newline, silently breaking code.

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

function Write-Step {
    param([Parameter(Mandatory)][string]$Message)
    Write-Host "`n==> $Message"
}

function Assert-CommandAvailable {
    param([Parameter(Mandatory)][string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "$Name is required but was not found on PATH."
    }
}

# 1. JWT key pair --------------------------------------------------------------
Write-Step 'Ensure JWT key pair'
$keyDirectory = Join-Path -Path $repositoryRoot -ChildPath 'apps/gin-backend/configs'
$privateKeyPath = Join-Path -Path $keyDirectory -ChildPath 'rsa_private.pem'
$publicKeyPath = Join-Path -Path $keyDirectory -ChildPath 'rsa_public.pem'

if ($Force -or -not (Test-Path -LiteralPath $privateKeyPath -PathType Leaf) -or -not (Test-Path -LiteralPath $publicKeyPath -PathType Leaf)) {
    Assert-CommandAvailable -Name 'go'
    # pemgenerator writes to -out (default 'configs') relative to its working directory,
    # so it must run from apps/gin-backend -- running it from the repository root would
    # scatter a stray top-level configs/ directory and leave the real path untouched.
    $backendDirectory = Join-Path -Path $repositoryRoot -ChildPath 'apps/gin-backend'
    Push-Location $backendDirectory
    try {
        & go run ./cmd/tools/pemgenerator
        if ($LASTEXITCODE -ne 0) {
            throw "pemgenerator failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
    # Never trust the generator's own report: confirm the files landed where the
    # container mount and config.docker.yaml expect them.
    foreach ($expected in @($privateKeyPath, $publicKeyPath)) {
        if (-not (Test-Path -LiteralPath $expected -PathType Leaf)) {
            throw "pemgenerator did not produce the expected key file: $expected"
        }
    }
    Write-Host "generated: $privateKeyPath"
}
else {
    Write-Host "already present: $privateKeyPath"
}

# 2. pg_search offline artifact ------------------------------------------------
Write-Step 'Ensure pg_search offline package'
$vendorDirectory = Join-Path -Path $repositoryRoot -ChildPath 'deployments/postgresql/vendor'
$debName = 'postgresql-17-pg-search_0.25.2-1PARADEDB-bookworm_amd64.deb'
$debPath = Join-Path -Path $vendorDirectory -ChildPath $debName
$expectedSha256 = 'f9f4cccbd5c19b8181c04bfb410bf64f76cac621fff62ca2e9086f220de1020a'

function Test-PgSearchDeb {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return $false
    }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash
    return $actual -eq $expectedSha256.ToUpperInvariant()
}

if ($Force -or -not (Test-PgSearchDeb -Path $debPath)) {
    Write-Host "downloading: $PgSearchUrl"
    New-Item -ItemType Directory -Path $vendorDirectory -Force | Out-Null
    $temporary = "$debPath.download"
    try {
        Invoke-WebRequest -Uri $PgSearchUrl -OutFile $temporary -UseBasicParsing
        $downloaded = (Get-FileHash -Algorithm SHA256 -LiteralPath $temporary).Hash
        if ($downloaded -ne $expectedSha256.ToUpperInvariant()) {
            throw "sha256 mismatch. expected=$expectedSha256 actual=$downloaded"
        }
        Move-Item -LiteralPath $temporary -Destination $debPath -Force
    }
    finally {
        Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
    }
    Write-Host "verified and stored: $debPath"
}
else {
    Write-Host "already present and verified: $debPath"
}

# 3. Toolchain report ----------------------------------------------------------
Write-Step 'Report optional toolchain'
foreach ($tool in @('docker', 'go', 'protoc', 'protoc-gen-go', 'protoc-gen-go-grpc', 'node', 'npm')) {
    $found = Get-Command $tool -ErrorAction SilentlyContinue
    if ($found) {
        Write-Host ("  {0,-22} {1}" -f $tool, 'ok')
    }
    else {
        Write-Host ("  {0,-22} {1}" -f $tool, 'MISSING (needed by some gates, not by docker compose up)')
    }
}

Write-Host "`nBOOTSTRAP=PASS"
