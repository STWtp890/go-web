[CmdletBinding()]
param(
    [switch]$KeepEnvironment,
    [ValidateRange(30, 600)]
    [int]$WaitTimeoutSeconds = 120
)

$ErrorActionPreference = 'Stop'
$moduleRoot = $PSScriptRoot
$composeFile = Join-Path $moduleRoot 'compose.index-delivery.yaml'
$projectName = 'gin-index-delivery-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
$previousPort = $env:INDEX_DELIVERY_POSTGRES_PORT
$previousDSN = $env:DOCUMENT_REPOSITORY_TEST_DSN
$previousIntegration = $env:INDEX_DELIVERY_STORE_INTEGRATION
$previousMaintenanceIntegration = $env:INDEX_MAINTENANCE_INTEGRATION
$started = $false

function Invoke-CheckedCommand {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][scriptblock]$Command
    )

    Write-Host ""
    Write-Host "==> $Label"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Label failed with exit code $LASTEXITCODE"
    }
}

$portProbe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$portProbe.Start()
$postgresPort = ([System.Net.IPEndPoint]$portProbe.LocalEndpoint).Port
$portProbe.Stop()

Push-Location $moduleRoot
try {
    $env:INDEX_DELIVERY_POSTGRES_PORT = [string]$postgresPort
    $env:DOCUMENT_REPOSITORY_TEST_DSN = "host=127.0.0.1 port=$postgresPort user=postgres password=postgres dbname=gin_demo sslmode=disable"
    $env:INDEX_DELIVERY_STORE_INTEGRATION = '1'
    $composePrefix = @('compose', '-p', $projectName, '-f', $composeFile)

    Invoke-CheckedCommand 'Validate P2.3 PostgreSQL Compose configuration' {
        docker @composePrefix config --quiet
    }
    $started = $true
    Invoke-CheckedCommand 'Start a fresh P2.3 PostgreSQL baseline' {
        docker @composePrefix up -d --build --wait --wait-timeout $WaitTimeoutSeconds postgres
    }
    Invoke-CheckedCommand 'Verify ordered claim, lease fencing, retry, dead letter, and replay' {
        go test ./internal/modules/document/infrastructure/postgresql -run TestIndexDeliveryStoreIntegration -count=1 -v
    }
    $env:INDEX_MAINTENANCE_INTEGRATION = '1'
    Invoke-CheckedCommand 'Verify reconciliation and repeatable-read rebuild preparation' {
        go test ./internal/modules/document/application -run TestIndexMaintenanceIntegration -count=1 -v
    }
    $env:INDEX_MAINTENANCE_INTEGRATION = ''
    Invoke-CheckedCommand 'Verify transactional document Outbox' {
        go test ./internal/modules/document/application -run TestCommandServiceIntegration -count=1 -v
    }
    $env:INDEX_DELIVERY_STORE_INTEGRATION = ''
    $env:DOCUMENT_REPOSITORY_TEST_DSN = ''
    Invoke-CheckedCommand 'Run document module regression tests' {
        go test ./internal/modules/document/... -count=1
    }

    Write-Host ""
    Write-Host 'P2.3_INDEX_DELIVERY=PASS'
}
finally {
    $env:INDEX_DELIVERY_POSTGRES_PORT = $previousPort
    $env:DOCUMENT_REPOSITORY_TEST_DSN = $previousDSN
    $env:INDEX_DELIVERY_STORE_INTEGRATION = $previousIntegration
    $env:INDEX_MAINTENANCE_INTEGRATION = $previousMaintenanceIntegration
    if (-not $KeepEnvironment -and $started) {
        & docker compose -p $projectName -f $composeFile down --volumes --remove-orphans
    }
    Pop-Location
}
