[CmdletBinding()]
param(
    [switch]$KeepEnvironment,
    [ValidateRange(30, 600)]
    [int]$WaitTimeoutSeconds = 120
)

$ErrorActionPreference = 'Stop'
$moduleRoot = $PSScriptRoot
$composeFile = Join-Path $moduleRoot 'compose.yaml'
$projectName = 'mixin-qdrant-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
$previousHTTPPort = $env:QDRANT_HTTP_PORT
$previousGRPCPort = $env:QDRANT_GRPC_PORT
$previousIntegration = $env:QDRANT_INTEGRATION
$previousHost = $env:QDRANT_HOST
$previousPort = $env:QDRANT_PORT
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

$httpProbe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$grpcProbe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$httpProbe.Start()
$grpcProbe.Start()
$httpPort = ([System.Net.IPEndPoint]$httpProbe.LocalEndpoint).Port
$grpcPort = ([System.Net.IPEndPoint]$grpcProbe.LocalEndpoint).Port
$httpProbe.Stop()
$grpcProbe.Stop()

Push-Location $moduleRoot
try {
    $env:QDRANT_HTTP_PORT = [string]$httpPort
    $env:QDRANT_GRPC_PORT = [string]$grpcPort
    $env:QDRANT_INTEGRATION = '1'
    $env:QDRANT_HOST = '127.0.0.1'
    $env:QDRANT_PORT = [string]$grpcPort
    $composePrefix = @('compose', '-p', $projectName, '-f', $composeFile)

    Invoke-CheckedCommand 'Validate mixin-search Compose configuration' {
        docker @composePrefix config --quiet
    }
    $started = $true
    Invoke-CheckedCommand 'Start a fresh Qdrant candidate store' {
        docker @composePrefix up -d --wait --wait-timeout $WaitTimeoutSeconds qdrant
    }
    Invoke-CheckedCommand 'Run Qdrant storage and document-control integration tests' {
        go test ./internal/rag -run 'TestQdrant.*Integration' -count=1 -v
    }
    # The alias suite is named differently from the TestQdrant*Integration family,
    # so it needs its own selection: without this the store-level alias evidence
    # cited by the plan would never run in any scripted gate.
    Invoke-CheckedCommand 'Run the stable-alias integration tests' {
        go test ./internal/rag -run 'TestQdrantAliasSwitchIsPerCorpus|TestAliasSwitchStaysInsideItsOwnCorpus' -count=1 -v
    }

    Write-Host ""
    Write-Host 'P2.2_QDRANT_CONTROL=PASS'
}
finally {
    $env:QDRANT_HTTP_PORT = $previousHTTPPort
    $env:QDRANT_GRPC_PORT = $previousGRPCPort
    $env:QDRANT_INTEGRATION = $previousIntegration
    $env:QDRANT_HOST = $previousHost
    $env:QDRANT_PORT = $previousPort
    if (-not $KeepEnvironment -and $started) {
        & docker compose -p $projectName -f $composeFile down --volumes --remove-orphans
    }
    Pop-Location
}
