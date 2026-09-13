[CmdletBinding()]
param(
    [switch]$SkipImageBuild,
    [switch]$KeepEnvironment,
    [ValidateRange(30, 1800)]
    [int]$WaitTimeoutSeconds = 300
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$composeFile = Join-Path $repositoryRoot 'docker-compose.yaml'
$projectName = 'go-web-p15-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
$composePrefix = @('compose', '-p', $projectName, '-f', $composeFile)
$previousGoCache = $env:GOCACHE
$previousDocumentDSN = $env:DOCUMENT_REPOSITORY_TEST_DSN
$verificationGoCache = Join-Path ([System.IO.Path]::GetTempPath()) 'go-web-build-verify-cache'
$started = $false

function Invoke-CheckedCommand {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][scriptblock]$Command
    )

    Write-Host "`n==> $Label"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Label failed with exit code $LASTEXITCODE"
    }
}

Push-Location $repositoryRoot
try {
    Invoke-CheckedCommand 'Validate Docker Compose configuration' {
        docker @composePrefix config --quiet
    }
    Invoke-CheckedCommand 'Verify generated mixin-search/v1 RPC code' {
        & ./packages/proto/verify-generated.ps1
    }

    Push-Location 'apps/simple-frontend'
    try {
        Invoke-CheckedCommand 'Type-check and build Vue frontend' { npm run build }
    }
    finally {
        Pop-Location
    }

    $composeArguments = $composePrefix + @('up', '-d')
    if (-not $SkipImageBuild) {
        $composeArguments += '--build'
    }
    $composeArguments += @('--wait', '--wait-timeout', $WaitTimeoutSeconds)
    Invoke-CheckedCommand 'Build and start a fresh disposable application stack' {
        docker @composeArguments
    }
    $started = $true

    Invoke-CheckedCommand 'Verify PostgreSQL development baseline' {
        docker @composePrefix exec -T postgres psql -X -v ON_ERROR_STOP=1 -U postgres -d gin_demo -f /database/sql/plugin/bm25_only_verify.sql
    }
    Invoke-CheckedCommand 'Seed disposable runtime-test manager' {
        docker @composePrefix exec -T postgres psql -X -v ON_ERROR_STOP=1 -U postgres -d gin_demo -v admin_user=p15_admin -v admin_pass=P1_5AdminPass234 -f /database/sql/service/manager/seed_admin.sql
    }

    $env:GOCACHE = $verificationGoCache
    $env:DOCUMENT_REPOSITORY_TEST_DSN = 'host=127.0.0.1 port=15432 user=postgres password=postgres dbname=gin_demo sslmode=disable'

    foreach ($module in @('apps/gin-backend', 'apps/mixin-search', 'packages/gen')) {
        Push-Location $module
        try {
            Invoke-CheckedCommand "Run $module Go tests" { go test ./... }
            Invoke-CheckedCommand "Run $module Go vet" { go vet ./... }
        }
        finally {
            Pop-Location
        }
    }

    Push-Location 'apps/gin-backend'
    try {
        $runID = 'p15_' + (Get-Date -Format 'yyyyMMdd_HHmmss')
        $reportDir = Join-Path $repositoryRoot 'deployments/test-results'
        Invoke-CheckedCommand 'Run deployed auth, document, manager, proxy, and disabled-Chat API verification' {
            go run ./cmd/runtimeapitest -run-id $runID -bootstrap-manager p15_admin -bootstrap-password P1_5AdminPass234 -report-dir $reportDir
        }
    }
    finally {
        Pop-Location
    }

    Write-Host "`n==> Verify HTTP entry points"
    foreach ($check in @(
        @{ Name = 'Frontend'; Uri = 'http://127.0.0.1:15173/' },
        @{ Name = 'Liveness through Nginx'; Uri = 'http://127.0.0.1:15173/healthz' },
        @{ Name = 'Readiness through Nginx'; Uri = 'http://127.0.0.1:15173/readyz' }
    )) {
        $response = Invoke-WebRequest -UseBasicParsing -Uri $check.Uri -TimeoutSec 10
        if ($response.StatusCode -ne 200) {
            throw "$($check.Name) returned HTTP $($response.StatusCode)"
        }
        Write-Host "PASS $($check.Name): HTTP $($response.StatusCode) $($check.Uri)"
    }

    Invoke-CheckedCommand 'Show final container state' {
        docker @composePrefix ps
    }
    Write-Host "`nP1.5_BUILD_TEST_DEPLOYMENT=PASS"
}
finally {
    $env:GOCACHE = $previousGoCache
    $env:DOCUMENT_REPOSITORY_TEST_DSN = $previousDocumentDSN
    if (-not $KeepEnvironment -and $started) {
        & docker @composePrefix down --volumes --remove-orphans
    }
    Pop-Location
}
