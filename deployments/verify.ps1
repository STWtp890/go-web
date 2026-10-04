[CmdletBinding()]
param(
    [switch]$SkipServices,
    [switch]$SkipFrontend,
    [ValidateRange(30, 1800)]
    [int]$ReadyTimeoutSeconds = 120
)

# Full local gate for the current architecture.
#
# This file replaces the P1.5/P2.3/P2.4/P2.5 gate that drove mixin-search's shadow
# index through document-index-worker, document-index-admin and
# document-search-eval, and that asserted the public.* projection, its delivery
# ledger and the shadow observations. Those entry points were superseded: formal
# documents are written by document-service, indexed by document-search's own
# consumer, and the old projection and its delivery machinery were removed in
# ADR-017 stage C. Driving a deleted binary would fail for the wrong reason, so
# the gate now runs the checks that describe the repository as it is:
#
#   contract  - generated code matches every committed .proto
#   modules   - build / vet / gofmt / test for each Go module
#   database  - the write-permission matrix between the service roles
#   services  - the three source-owned services over real gRPC
#   web       - create/detail/save/policy/list/search/delete over real HTTP auth
#   frontend  - phase boundary, type-check and production build
#   docs      - every documentation link resolves
#
# The mixin-search corpus checks that survived (control-plane isolation, chat
# alias switching) now live with that module: see apps/mixin-search/verify-*.ps1.
# They are not part of this gate because they need their own Compose project.
#
# Keep this file ASCII-only. Windows PowerShell 5.1 parses a BOM-less .ps1 as
# ANSI, and a multi-byte comment can swallow the following newline.

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$previousGoCache = $env:GOCACHE
$started = $false

function Write-Step {
    param([Parameter(Mandatory)][string]$Message)
    Write-Host "`n==> $Message"
}

function Assert-FilePresent {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$Label)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "$Label is missing: $Path (run deployments/bootstrap.ps1)"
    }
    Write-Host "  present: $Path"
}

function Invoke-CheckedCommand {
    param([Parameter(Mandatory)][string]$Label, [Parameter(Mandatory)][scriptblock]$Command)

    # Native tools write progress to stderr, which is normal. Windows PowerShell 5.1
    # turns those lines into NativeCommandError records once the script's own output
    # is redirected, and with $ErrorActionPreference='Stop' that terminates the run.
    Write-Host "`n==> $Label"
    $previousErrorAction = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $Command
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorAction
    }
    if ($exitCode -ne 0) {
        throw "$Label failed with exit code $exitCode"
    }
}

Push-Location $repositoryRoot
try {
    Write-Host "repository: $repositoryRoot"

    Assert-FilePresent -Path (Join-Path $repositoryRoot 'deployments/secrets/mixin_search_capability.key') -Label 'boundary key'
    Assert-FilePresent -Path (Join-Path $repositoryRoot 'apps/gin-backend/configs/rsa_private.pem') -Label 'JWT private key'
    Assert-FilePresent -Path (Join-Path $repositoryRoot 'apps/gin-backend/configs/rsa_public.pem') -Label 'JWT public key'

    Invoke-CheckedCommand 'Verify generated protocol code matches every committed .proto' {
        & ./packages/proto/verify-generated.ps1
    }

    # The Go build cache lives outside the repository so a second checkout cannot
    # collide with a running one, and so a cached entry owned by another user
    # cannot fail the gate with a permission error.
    $env:GOCACHE = Join-Path ([System.IO.Path]::GetTempPath()) 'gb-goweb'

    foreach ($module in @(
            'apps/document-service',
            'apps/document-search',
            'apps/qq-search',
            'apps/mixin-search',
            'apps/gin-backend',
            'packages/gen',
            'packages/serviceauth'
        )) {
        Push-Location $module
        try {
            Invoke-CheckedCommand "Build, vet and test $module" {
                go build ./...
                if ($LASTEXITCODE -ne 0) { throw "go build failed in $module" }
                go vet ./...
                if ($LASTEXITCODE -ne 0) { throw "go vet failed in $module" }
                $unformatted = @(gofmt -l .)
                if ($unformatted.Count -gt 0) {
                    $unformatted | ForEach-Object { Write-Host "      unformatted: $_" }
                    throw "gofmt reports $($unformatted.Count) unformatted file(s) in $module"
                }
                go test ./... -count=1
            }
        }
        finally {
            Pop-Location
        }
    }

    Invoke-CheckedCommand 'Verify the database write-permission matrix' {
        & ./deployments/postgresql/verify-service-isolation.ps1
    }

    if ($SkipServices) {
        Write-Host "`n==> Skipping the gates that start real services (-SkipServices)"
    }
    else {
        Invoke-CheckedCommand 'Verify the source-owned services over real gRPC' {
            & ./deployments/verify-source-owned-services.ps1 -ReadyTimeoutSeconds $ReadyTimeoutSeconds
        }
        $started = $true

        Invoke-CheckedCommand 'Verify the Web document chain over real HTTP authentication' {
            & ./deployments/verify-stage-a-web.ps1 -ReadyTimeoutSeconds $ReadyTimeoutSeconds
        }
    }

    if ($SkipFrontend) {
        Write-Host "`n==> Skipping the frontend build (-SkipFrontend)"
    }
    else {
        Push-Location 'apps/simple-frontend'
        try {
            if (-not (Test-Path -LiteralPath 'node_modules')) {
                Invoke-CheckedCommand 'Install frontend dependencies' { npm ci }
            }
            # build script chain: phase boundary check -> vue-tsc -> vite build
            Invoke-CheckedCommand 'Type-check and build the Vue frontend' { npm run build }
        }
        finally {
            Pop-Location
        }
    }

    Invoke-CheckedCommand 'Verify documentation links' {
        & ./docs/check-doc-links.ps1
    }

    Write-Host "`nVERIFY=PASS"
}
finally {
    $env:GOCACHE = $previousGoCache
    Pop-Location
}
