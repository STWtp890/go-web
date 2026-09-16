[CmdletBinding()]
param(
    [switch]$KeepEnvironment,
    [ValidateRange(30, 600)]
    [int]$WaitTimeoutSeconds = 120
)

$ErrorActionPreference = 'Stop'
$moduleRoot = $PSScriptRoot
$composeFile = Join-Path $moduleRoot 'compose.yaml'
$projectName = 'mixin-control-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
$previousPort = $env:CONTROL_POSTGRES_PORT
$previousIntegration = $env:CONTROL_STORE_INTEGRATION
$previousDSN = $env:CONTROL_DATABASE_DSN
$previousBootstrap = $env:CONTROL_STORE_BOOTSTRAP
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
$controlPort = ([System.Net.IPEndPoint]$portProbe.LocalEndpoint).Port
$portProbe.Stop()

Push-Location $moduleRoot
try {
    $env:CONTROL_POSTGRES_PORT = [string]$controlPort
    $env:CONTROL_STORE_INTEGRATION = '1'
	$env:CONTROL_DATABASE_DSN = "postgres://mixin_control:mixin_control@127.0.0.1:$controlPort/mixin_control?sslmode=disable"
	$env:CONTROL_STORE_BOOTSTRAP = '1'
    $composePrefix = @('compose', '-p', $projectName, '-f', $composeFile)

    Invoke-CheckedCommand 'Validate mixin-search Compose configuration' {
        docker @composePrefix config --quiet
    }
    $started = $true
    Invoke-CheckedCommand 'Start a fresh control PostgreSQL' {
        docker @composePrefix up -d --wait --wait-timeout $WaitTimeoutSeconds control-postgres
    }
    Invoke-CheckedCommand 'Run PostgreSQL control-store restart and CAS integration tests' {
        go test ./internal/rag -run TestPostgresControlStoreIntegration -count=1 -v
    }

    Write-Host ""
    Write-Host "P2.1_CONTROL_STORE=PASS"
}
finally {
    $env:CONTROL_POSTGRES_PORT = $previousPort
    $env:CONTROL_STORE_INTEGRATION = $previousIntegration
	$env:CONTROL_DATABASE_DSN = $previousDSN
	$env:CONTROL_STORE_BOOTSTRAP = $previousBootstrap
    if (-not $KeepEnvironment -and $started) {
        & docker compose -p $projectName -f $composeFile down --volumes --remove-orphans
    }
    Pop-Location
}
