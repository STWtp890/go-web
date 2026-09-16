[CmdletBinding()]
param(
    [switch]$KeepEnvironment,
    [ValidateRange(30, 600)]
    [int]$WaitTimeoutSeconds = 120
)

$ErrorActionPreference = 'Stop'
$moduleRoot = $PSScriptRoot
$repositoryRoot = (Resolve-Path (Join-Path $moduleRoot '../..')).Path
$mixinRoot = Join-Path $repositoryRoot 'apps/mixin-search'
$ginComposeFile = Join-Path $moduleRoot 'compose.index-delivery.yaml'
$mixinComposeFile = Join-Path $mixinRoot 'compose.yaml'
$ginProject = 'gin-index-e2e-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
$mixinProject = 'mixin-index-e2e-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
$ginStarted = $false
$mixinStarted = $false
$serverProcess = $null
$temporaryPrefix = Join-Path ([System.IO.Path]::GetTempPath()) ('go-web-p23-' + [guid]::NewGuid().ToString('N'))
$serverExe = $temporaryPrefix + '.exe'
$serverOut = $temporaryPrefix + '.out.log'
$serverErr = $temporaryPrefix + '.err.log'

$environmentNames = @(
    'INDEX_DELIVERY_POSTGRES_PORT', 'DOCUMENT_REPOSITORY_TEST_DSN',
    'INDEX_DELIVERY_E2E', 'MIXIN_SEARCH_E2E_ADDRESS',
    'QDRANT_HTTP_PORT', 'QDRANT_GRPC_PORT', 'CONTROL_POSTGRES_PORT'
)
$previousEnvironment = @{}
foreach ($name in $environmentNames) {
    $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name)
}

function Get-FreeTcpPort {
    $probe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $probe.Start()
    $port = ([System.Net.IPEndPoint]$probe.LocalEndpoint).Port
    $probe.Stop()
    return $port
}

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

function Wait-TcpPort {
    param([int]$Port, [int]$TimeoutSeconds)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($null -ne $serverProcess -and $serverProcess.HasExited) {
            throw "mixin-search exited before port $Port became ready"
        }
        $client = [System.Net.Sockets.TcpClient]::new()
        try {
            $connect = $client.ConnectAsync('127.0.0.1', $Port)
            if ($connect.Wait(500) -and $client.Connected) {
                return
            }
        }
        catch {
        }
        finally {
            $client.Dispose()
        }
        Start-Sleep -Milliseconds 200
    }
    throw "timed out waiting for 127.0.0.1:$Port"
}

$ginPostgresPort = Get-FreeTcpPort
$qdrantHTTPPort = Get-FreeTcpPort
$qdrantGRPCPort = Get-FreeTcpPort
$controlPostgresPort = Get-FreeTcpPort
$ragGRPCPort = Get-FreeTcpPort

Push-Location $moduleRoot
try {
    $env:INDEX_DELIVERY_POSTGRES_PORT = [string]$ginPostgresPort
    $env:QDRANT_HTTP_PORT = [string]$qdrantHTTPPort
    $env:QDRANT_GRPC_PORT = [string]$qdrantGRPCPort
    $env:CONTROL_POSTGRES_PORT = [string]$controlPostgresPort
    $env:DOCUMENT_REPOSITORY_TEST_DSN = "host=127.0.0.1 port=$ginPostgresPort user=postgres password=postgres dbname=gin_demo sslmode=disable"
    $env:INDEX_DELIVERY_E2E = '1'
    $env:MIXIN_SEARCH_E2E_ADDRESS = "127.0.0.1:$ragGRPCPort"

    $ginCompose = @('compose', '-p', $ginProject, '-f', $ginComposeFile)
    $mixinCompose = @('compose', '-p', $mixinProject, '-f', $mixinComposeFile)
    Invoke-CheckedCommand 'Validate P2.3 rebuild Compose files' {
        docker @ginCompose config --quiet
        if ($LASTEXITCODE -eq 0) { docker @mixinCompose config --quiet }
    }
    $ginStarted = $true
    Invoke-CheckedCommand 'Start fresh go-web PostgreSQL' {
        docker @ginCompose up -d --build --wait --wait-timeout $WaitTimeoutSeconds postgres
    }
    $mixinStarted = $true
    Invoke-CheckedCommand 'Start empty mixin-search control PostgreSQL and Qdrant' {
        docker @mixinCompose up -d --wait --wait-timeout $WaitTimeoutSeconds control-postgres qdrant
    }
    Push-Location $mixinRoot
    try {
        Invoke-CheckedCommand 'Build isolated mixin-search server' {
            go build -o $serverExe ./cmd/rag-server
        }
    }
    finally {
        Pop-Location
    }

    $serverArguments = @(
        '-grpc-address', "127.0.0.1:$ragGRPCPort",
        '-store', 'qdrant', '-qdrant-host', '127.0.0.1', '-qdrant-port', [string]$qdrantGRPCPort,
        '-qdrant-collection', 'p23_rebuild',
        '-control-store', 'postgres',
        '-control-dsn', "postgres://mixin_control:mixin_control@127.0.0.1:$controlPostgresPort/mixin_control?sslmode=disable",
        '-control-namespace', 'p23-rebuild', '-control-bootstrap=true'
    )
    $serverProcess = Start-Process -FilePath $serverExe -ArgumentList $serverArguments `
        -WorkingDirectory $mixinRoot -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput $serverOut -RedirectStandardError $serverErr
    Wait-TcpPort -Port $ragGRPCPort -TimeoutSeconds $WaitTimeoutSeconds

    Invoke-CheckedCommand 'Rebuild empty control store and Qdrant from gin-backend facts' {
        go test ./internal/modules/document/application -run TestIndexDeliveryRebuildE2E -count=1 -v
    }
    Write-Host ""
    Write-Host 'P2.3_INDEX_REBUILD_E2E=PASS'
}
catch {
    if (Test-Path -LiteralPath $serverErr) {
        Write-Host ""
        Write-Host '==> mixin-search stderr'
        Get-Content -LiteralPath $serverErr
    }
    throw
}
finally {
    if ($null -ne $serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -Force
        $serverProcess.WaitForExit()
    }
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name])
    }
    if (-not $KeepEnvironment) {
        if ($mixinStarted) {
            & docker compose -p $mixinProject -f $mixinComposeFile down --volumes --remove-orphans
        }
        if ($ginStarted) {
            & docker compose -p $ginProject -f $ginComposeFile down --volumes --remove-orphans
        }
    }
    foreach ($path in @($serverExe, $serverOut, $serverErr)) {
        if (Test-Path -LiteralPath $path) {
            Remove-Item -LiteralPath $path -Force
        }
    }
    Pop-Location
}
