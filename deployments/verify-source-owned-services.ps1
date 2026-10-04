[CmdletBinding()]
param(
    [int]$DocumentServiceGRPCPort = 28081,
    [int]$DocumentServiceHTTPPort = 28091,
    [int]$DocumentSearchGRPCPort = 28082,
    [int]$DocumentSearchHTTPPort = 28092,
    [int]$QQSearchGRPCPort = 28083,
    [int]$QQSearchHTTPPort = 28093,
    [string]$PostgresHost = '127.0.0.1',
    [int]$PostgresPort = 15432,
    [string]$Database = 'gin_demo',
    [string]$BoundaryKeyFile = '',
    [int]$ReadyTimeoutSeconds = 90,
    [int]$ChainTimeoutSeconds = 60,
    [switch]$KeepRunning
)

# Cross-service acceptance gate for ADR-017.
#
# It starts the three source-owned services as real processes against the
# development PostgreSQL, waits for their readiness probes, and then runs the
# acceptance client that drives the two chains over real gRPC:
#
#   go-web business command --> document-service --> Outbox --> document-search
#   py-agent raw events     --> qq-search
#
# Every step asserts a real observable result. A service that fails to start, a
# probe that never becomes ready, or a chain that does not complete fails the
# gate; nothing here is allowed to degrade into a skip.
#
# Keep this file ASCII-only. Windows PowerShell 5.1 parses a BOM-less .ps1 as
# ANSI, and a multi-byte comment can swallow the following newline.

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$logDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ('adr017-e2e-' + [guid]::NewGuid().ToString('N'))

if ([string]::IsNullOrWhiteSpace($BoundaryKeyFile)) {
    $BoundaryKeyFile = Join-Path $repositoryRoot 'deployments/secrets/mixin_search_capability.key'
}
if (-not (Test-Path -LiteralPath $BoundaryKeyFile -PathType Leaf)) {
    throw "boundary key is missing: $BoundaryKeyFile (run deployments/bootstrap.ps1)"
}

function Write-Step {
    param([Parameter(Mandatory)][string]$Message)
    Write-Host "`n==> $Message"
}

function New-ServiceProcess {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$ModuleDirectory,
        [Parameter(Mandatory)][string]$Binary,
        [Parameter(Mandatory)][hashtable]$Environment,
        [Parameter(Mandatory)][string]$ProbeUrl
    )

    $modulePath = Join-Path $repositoryRoot $ModuleDirectory
    $binaryPath = Join-Path $logDirectory ($Name + '.exe')
    # The build must run inside the module directory: `./cmd/...` is relative to
    # the working directory, and the module (not the repository root) is what owns
    # those packages.
    Push-Location $modulePath
    try {
        $buildOutput = & go build -o $binaryPath "./$Binary" 2>&1
        $buildExit = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
    if ($buildExit -ne 0) {
        throw "building $Name failed: $buildOutput"
    }

    $stdout = Join-Path $logDirectory ($Name + '.out.log')
    $stderr = Join-Path $logDirectory ($Name + '.err.log')
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $binaryPath
    $startInfo.WorkingDirectory = $modulePath
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    foreach ($key in $Environment.Keys) {
        $startInfo.EnvironmentVariables[$key] = $Environment[$key]
    }
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    # The redirect handlers are attached through events so a chatty service cannot
    # fill a pipe buffer and deadlock the gate.
    $null = Register-ObjectEvent -InputObject $process -EventName OutputDataReceived -Action {
        if ($EventArgs.Data) { Add-Content -LiteralPath $Event.MessageData -Value $EventArgs.Data }
    } -MessageData $stdout
    $null = Register-ObjectEvent -InputObject $process -EventName ErrorDataReceived -Action {
        if ($EventArgs.Data) { Add-Content -LiteralPath $Event.MessageData -Value $EventArgs.Data }
    } -MessageData $stderr
    if (-not $process.Start()) {
        throw "starting $Name failed"
    }
    $process.BeginOutputReadLine()
    $process.BeginErrorReadLine()

    Write-Host ("  {0,-18} pid={1} probe={2}" -f $Name, $process.Id, $ProbeUrl)
    return [pscustomobject]@{ Name = $Name; Process = $process; ProbeUrl = $ProbeUrl; Stdout = $stdout; Stderr = $stderr }
}

function Wait-Probe {
    param(
        [Parameter(Mandatory)]$Service,
        [Parameter(Mandatory)][int]$TimeoutSeconds
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if ($Service.Process.HasExited) {
            $tail = if (Test-Path -LiteralPath $Service.Stderr) { Get-Content -LiteralPath $Service.Stderr -Tail 20 | Out-String } else { '' }
            throw "$($Service.Name) exited with code $($Service.Process.ExitCode) before becoming ready. stderr:`n$tail"
        }
        try {
            $response = Invoke-WebRequest -Uri $Service.ProbeUrl -TimeoutSec 3 -UseBasicParsing
            if ($response.StatusCode -eq 200) {
                Write-Host "  $($Service.Name) ready"
                return
            }
        }
        catch {
            Start-Sleep -Milliseconds 300
        }
    }
    $tail = if (Test-Path -LiteralPath $Service.Stderr) { Get-Content -LiteralPath $Service.Stderr -Tail 20 | Out-String } else { '' }
    throw "$($Service.Name) did not become ready within $TimeoutSeconds s. stderr:`n$tail"
}

function Stop-ServiceProcess {
    param($Service)
    if ($null -eq $Service) { return }
    try {
        if (-not $Service.Process.HasExited) {
            $Service.Process.Kill()
            $null = $Service.Process.WaitForExit(10000)
        }
    }
    catch {
        Write-Warning "stopping $($Service.Name) failed: $($_.Exception.Message)"
    }
}

New-Item -ItemType Directory -Path $logDirectory -Force | Out-Null
Write-Host "artifact directory: $logDirectory"

$documentServiceDsn = "postgres://document_service_writer:document_service@${PostgresHost}:${PostgresPort}/${Database}?sslmode=disable"
$documentSearchDsn = "postgres://document_search_writer:document_search@${PostgresHost}:${PostgresPort}/${Database}?sslmode=disable"
$qqSearchDsn = "postgres://qq_search_writer:qq_search@${PostgresHost}:${PostgresPort}/${Database}?sslmode=disable"

$documentService = $null
$documentSearch = $null
$qqSearch = $null

try {
    Write-Step 'Build and start document-service'
    $documentService = New-ServiceProcess -Name 'document-service' -ModuleDirectory 'apps/document-service' -Binary 'cmd/document-service' -ProbeUrl "http://127.0.0.1:$DocumentServiceHTTPPort/readyz" -Environment @{
        DOCUMENT_SERVICE_POSTGRES_DSN           = $documentServiceDsn
        DOCUMENT_SERVICE_CAPABILITY_KEY_FILE    = $BoundaryKeyFile
        DOCUMENT_SERVICE_GRPC_LISTEN_ADDRESS    = "127.0.0.1:$DocumentServiceGRPCPort"
        DOCUMENT_SERVICE_HTTP_LISTEN_ADDRESS    = "127.0.0.1:$DocumentServiceHTTPPort"
        DOCUMENT_SERVICE_ENABLE_REFLECTION       = 'false'
    }
    Wait-Probe -Service $documentService -TimeoutSeconds $ReadyTimeoutSeconds

    Write-Step 'Build and start document-search'
    $documentSearch = New-ServiceProcess -Name 'document-search' -ModuleDirectory 'apps/document-search' -Binary 'cmd/document-search' -ProbeUrl "http://127.0.0.1:$DocumentSearchHTTPPort/readyz" -Environment @{
        DOCUMENT_SEARCH_POSTGRES_DSN                  = $documentSearchDsn
        DOCUMENT_SEARCH_CAPABILITY_KEY_FILE           = $BoundaryKeyFile
        DOCUMENT_SEARCH_SOURCE_CAPABILITY_KEY_FILE    = $BoundaryKeyFile
        DOCUMENT_SEARCH_GRPC_LISTEN_ADDRESS           = "127.0.0.1:$DocumentSearchGRPCPort"
        DOCUMENT_SEARCH_HTTP_LISTEN_ADDRESS           = "127.0.0.1:$DocumentSearchHTTPPort"
        DOCUMENT_SEARCH_SOURCE_ENDPOINT               = "127.0.0.1:$DocumentServiceGRPCPort"
        DOCUMENT_SEARCH_SOURCE_CALLER_IDENTITY        = 'document-service'
        DOCUMENT_SEARCH_SOURCE_AUDIENCE               = 'document-service'
        DOCUMENT_SEARCH_SOURCE_SCOPE                  = 'document-event-reader'
    }
    Wait-Probe -Service $documentSearch -TimeoutSeconds $ReadyTimeoutSeconds

    Write-Step 'Build and start qq-search'
    $qqSearch = New-ServiceProcess -Name 'qq-search' -ModuleDirectory 'apps/qq-search' -Binary 'cmd/qq-search' -ProbeUrl "http://127.0.0.1:$QQSearchHTTPPort/readyz" -Environment @{
        QQ_SEARCH_POSTGRES_DSN           = $qqSearchDsn
        QQ_SEARCH_CAPABILITY_KEY_FILE    = $BoundaryKeyFile
        QQ_SEARCH_GRPC_LISTEN_ADDRESS    = "127.0.0.1:$QQSearchGRPCPort"
        QQ_SEARCH_HTTP_LISTEN_ADDRESS    = "127.0.0.1:$QQSearchHTTPPort"
    }
    Wait-Probe -Service $qqSearch -TimeoutSeconds $ReadyTimeoutSeconds

    Write-Step 'Run the cross-service acceptance client'
    $clientDirectory = Join-Path $repositoryRoot 'apps/document-service'
    # The client is invoked through an explicit process rather than the call
    # operator: the acceptance client logs to stderr through slog, and Windows
    # PowerShell 5.1 turns native stderr into a terminating error under
    # $ErrorActionPreference = 'Stop', which would abort the gate before its exit
    # code could be read.
    $clientArguments = @(
        'run', './cmd/document-service-e2e',
        '-document-service', "127.0.0.1:$DocumentServiceGRPCPort",
        '-document-search', "127.0.0.1:$DocumentSearchGRPCPort",
        '-qq-search', "127.0.0.1:$QQSearchGRPCPort",
        '-capability-key-file', $BoundaryKeyFile,
        '-wait', "${ChainTimeoutSeconds}s"
    )
    $clientInfo = New-Object System.Diagnostics.ProcessStartInfo
    $clientInfo.FileName = 'go'
    $clientInfo.Arguments = ($clientArguments | ForEach-Object { '"' + $_ + '"' }) -join ' '
    $clientInfo.WorkingDirectory = $clientDirectory
    $clientInfo.UseShellExecute = $false
    $clientInfo.RedirectStandardOutput = $true
    $clientInfo.RedirectStandardError = $true
    $clientProcess = New-Object System.Diagnostics.Process
    $clientProcess.StartInfo = $clientInfo
    if (-not $clientProcess.Start()) {
        throw 'could not start the acceptance client'
    }
    $clientStdout = $clientProcess.StandardOutput.ReadToEnd()
    $clientStderr = $clientProcess.StandardError.ReadToEnd()
    $clientProcess.WaitForExit()
    $chainExit = $clientProcess.ExitCode
    $chainOutput = @($clientStdout -split "`r?`n") + @($clientStderr -split "`r?`n")

    $chainOutput | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | ForEach-Object { Write-Host $_ }
    if ($chainExit -ne 0) {
        throw "the cross-service acceptance client failed with exit code $chainExit"
    }
    $chainText = $chainOutput -join "`n"
    if (($chainText -notmatch 'DOCUMENT_CHAIN_OK') -or ($chainText -notmatch 'QQ_CHAIN_OK')) {
        throw 'the acceptance client did not report both chains as OK'
    }
}
finally {
    if (-not $KeepRunning) {
        Write-Step 'Stop the source-owned services'
        Stop-ServiceProcess -Service $qqSearch
        Stop-ServiceProcess -Service $documentSearch
        Stop-ServiceProcess -Service $documentService
    }
    else {
        Write-Host "`n-KeepRunning was passed; services are still up. Logs: $logDirectory"
    }
}

Write-Host "`nADR017_E2E=PASS"
Write-Host "logs: $logDirectory"
