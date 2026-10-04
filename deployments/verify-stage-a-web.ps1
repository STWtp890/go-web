[CmdletBinding()]
param(
    [int]$DocumentServiceGRPCPort = 28081,
    [int]$DocumentServiceHTTPPort = 28091,
    [int]$DocumentSearchGRPCPort = 28082,
    [int]$DocumentSearchHTTPPort = 28092,
    [int]$BackendPort = 18080,
    [string]$PostgresHost = '127.0.0.1',
    [int]$PostgresPort = 15432,
    [string]$Database = 'gin_demo',
    [int]$RedisPort = 16379,
    [string]$BoundaryKeyFile = '',
    [int]$ReadyTimeoutSeconds = 120,
    [int]$IndexTimeoutSeconds = 90,
    [int]$PageSize = 9,
    [int]$DocumentCount = 25,
    [switch]$KeepRunning,
    # Runs the cookie-parsing self test instead of the acceptance, so both header
    # shapes can be checked on a host that has only one PowerShell edition.
    [switch]$SelfTest,
    # Drives an already running stack (for example the Compose containers)
    # instead of building and starting local processes. The acceptance itself is
    # identical, so the same assertions cover the deployed composition.
    [switch]$UseRunningStack
)

# Stage A end-to-end Web acceptance gate.
#
# It starts document-service, document-search and gin-backend as real processes
# against the development PostgreSQL, then drives the Web HTTP surface through
# real authentication:
#
#   browser session -> gin-backend -> document-service (commands, detail reads)
#                                  -> document-search  (capability-scoped search)
#
# Everything asserted here is an observable HTTP result. A missing service, an
# unready probe, or a chain that does not settle fails the gate; nothing is
# allowed to degrade into a skip.
#
# Keep this file ASCII-only. Windows PowerShell 5.1 parses a BOM-less .ps1 as
# ANSI, and a multi-byte comment can swallow the following newline.

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$workDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ('stage-a-web-' + [guid]::NewGuid().ToString('N'))

if ([string]::IsNullOrWhiteSpace($BoundaryKeyFile)) {
    $BoundaryKeyFile = Join-Path $repositoryRoot 'deployments/secrets/mixin_search_capability.key'
}
if (-not (Test-Path -LiteralPath $BoundaryKeyFile -PathType Leaf)) {
    throw "boundary key is missing: $BoundaryKeyFile (run deployments/bootstrap.ps1)"
}
$BoundaryKeyFile = (Resolve-Path -LiteralPath $BoundaryKeyFile).Path

$runID = [guid]::NewGuid().ToString('N').Substring(0, 12)
$baseURI = "http://127.0.0.1:$BackendPort"
$failureCount = 0
$assertionCount = 0

function Write-Step {
    param([Parameter(Mandatory)][string]$Message)
    Write-Host "`n==> $Message"
}

function Assert-That {
    param(
        [Parameter(Mandatory)][bool]$Condition,
        [Parameter(Mandatory)][string]$Description
    )
    $script:assertionCount++
    if ($Condition) {
        Write-Host "  PASS  $Description"
    }
    else {
        $script:failureCount++
        Write-Host "  FAIL  $Description" -ForegroundColor Red
    }
}

# --- process helpers ---------------------------------------------------------

function New-ServiceProcess {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$ModuleDirectory,
        [Parameter(Mandatory)][string]$Binary,
        [Parameter(Mandatory)][hashtable]$Environment,
        [Parameter(Mandatory)][string]$ProbeUrl
    )
    $modulePath = Join-Path $repositoryRoot $ModuleDirectory
    $binaryPath = Join-Path $workDirectory ($Name + '.exe')
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

    $stdout = Join-Path $workDirectory ($Name + '.out.log')
    $stderr = Join-Path $workDirectory ($Name + '.err.log')
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
    param([Parameter(Mandatory)]$Service, [Parameter(Mandatory)][int]$TimeoutSeconds)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if ($Service.Process.HasExited) {
            $tail = if (Test-Path -LiteralPath $Service.Stderr) { Get-Content -LiteralPath $Service.Stderr -Tail 30 | Out-String } else { '' }
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
    $tail = if (Test-Path -LiteralPath $Service.Stderr) { Get-Content -LiteralPath $Service.Stderr -Tail 30 | Out-String } else { '' }
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

# --- HTTP helpers ------------------------------------------------------------

# Read-SetCookieValues returns one string per Set-Cookie header.
#
# The two PowerShell editions expose response headers differently: Windows
# PowerShell 5.1 hands back a WebHeaderCollection whose Set-Cookie indexer joins
# every header into one string, while PowerShell 7 hands back an
# HttpResponseHeaders whose indexer yields a sequence of separate values and
# which has no GetValues method at all. Casting either shape to [string] loses
# the cookie boundaries, so each shape is enumerated instead, and a joined string
# is split on boundaries that can only be cookie starts - a cookie value never
# contains a comma, while the comma in an Expires date is never followed by
# "name=".
function Read-SetCookieValues {
    param($Headers)

    $values = New-Object System.Collections.Generic.List[string]
    if ($null -eq $Headers) { return $values }

    $raw = @()
    if ($Headers.PSObject.Methods['GetValues']) {
        # A header collection (WebHeaderCollection). Its enumerator yields header
        # NAMES, so it must be read through GetValues or the indexer, never by
        # iterating it.
        try { $raw = @($Headers.GetValues('Set-Cookie')) } catch { $raw = @() }
        if ($raw.Count -eq 0) {
            try { $raw = @($Headers['Set-Cookie']) } catch { $raw = @() }
        }
    }
    elseif ($Headers -is [string]) {
        $raw = @($Headers)
    }
    elseif ($Headers -is [System.Collections.IEnumerable] -and -not ($Headers -is [System.Collections.IDictionary])) {
        # Already a sequence of header values.
        $raw = @($Headers)
    }
    else {
        try { $raw = @($Headers['Set-Cookie']) } catch { $raw = @() }
    }

    foreach ($entry in $raw) {
        if ($null -eq $entry) { continue }
        if ($entry -is [string]) {
            foreach ($part in ($entry -split ',(?=\s*[A-Za-z0-9_\-]+=)')) {
                $trimmed = $part.Trim()
                if ($trimmed) { $values.Add($trimmed) }
            }
            continue
        }
        # An enumerable of header values (PowerShell 7's shape).
        foreach ($inner in $entry) {
            if ($null -ne $inner -and ([string]$inner).Trim()) { $values.Add(([string]$inner).Trim()) }
        }
    }
    return $values
}

# ConvertTo-CookieTable turns Set-Cookie header values into a name -> value table.
# Only the first name=value pair of each header is a cookie; the rest are
# attributes.
function ConvertTo-CookieTable {
    param($Values)

    $table = @{}
    foreach ($header in @($Values)) {
        $pair = ([string]$header -split ';')[0]
        $split = $pair.IndexOf('=')
        if ($split -le 0) { continue }
        $name = $pair.Substring(0, $split).Trim()
        if ($name) { $table[$name] = $pair.Substring($split + 1) }
    }
    return $table
}

# Get-SessionCookieTable collects the session cookies from both places a given
# edition may have put them: the login response headers, and the request
# session's cookie container. Taking both is what makes the gate independent of
# the edition - 5.1 keeps the path-scoped HttpOnly cookies only in the headers,
# and a host that stores them in the container but hides the header list is
# covered by the second source.
function Get-SessionCookieTable {
    param($Values, $Session)

    $table = ConvertTo-CookieTable (Read-SetCookieValues $Values)
    if ($Session) {
        foreach ($path in @('/api/v1/protected', '/')) {
            foreach ($cookie in $Session.Cookies.GetCookies([uri]($baseURI + $path))) {
                if (-not $table.ContainsKey($cookie.Name)) { $table[$cookie.Name] = $cookie.Value }
            }
        }
    }
    return $table
}

function Invoke-Api {
    param(
        [Parameter(Mandatory)][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        $Session,
        $Body,
        [hashtable]$Headers
    )
    $uri = $baseURI + $Path
    $parameters = @{
        Method     = $Method
        Uri        = $uri
        TimeoutSec = 30
        ErrorAction = 'Stop'
    }
    # Windows PowerShell 5.1 otherwise tries to parse responses with the Internet
    # Explorer engine and fails in a non-interactive host. PowerShell 7 removed
    # the switch's meaning, so it is only passed where it matters.
    if ($PSVersionTable.PSVersion.Major -lt 6) { $parameters.UseBasicParsing = $true }
    if ($Session) { $parameters.WebSession = $Session }
    if ($null -ne $Body) {
        $parameters.Body = ($Body | ConvertTo-Json -Depth 6 -Compress)
        $parameters.ContentType = 'application/json'
    }
    if ($Headers) { $parameters.Headers = $Headers }

    # The gate asserts on status codes and error bodies, so a non-2xx answer must
    # be returned as data. The recovery path is written for both editions: a
    # refusal carries its body on ErrorDetails in PowerShell 7 and on the
    # response stream in Windows PowerShell 5.1.
    $status = 0
    $content = ''
    $setCookie = @()
    try {
        $response = Invoke-WebRequest @parameters
        $status = [int]$response.StatusCode
        $content = [string]$response.Content
        $setCookie = @(Read-SetCookieValues $response.Headers)
    }
    catch {
        $webResponse = $_.Exception.Response
        if ($null -eq $webResponse) { throw }
        try { $status = [int]$webResponse.StatusCode } catch { $status = 0 }
        if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
            $content = [string]$_.ErrorDetails.Message
        }
        elseif ($webResponse.PSObject.Methods['GetResponseStream']) {
            $reader = New-Object System.IO.StreamReader($webResponse.GetResponseStream())
            try { $content = $reader.ReadToEnd() } finally { $reader.Dispose() }
        }
    }

    $parsed = $null
    if (-not [string]::IsNullOrWhiteSpace($content)) {
        try { $parsed = $content | ConvertFrom-Json } catch { $parsed = $null }
    }
    return [pscustomobject]@{ Status = $status; Body = $parsed; Raw = $content; SetCookie = $setCookie }
}

function New-Session {
    param([Parameter(Mandatory)][string]$Email, [Parameter(Mandatory)][string]$Nickname)
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $registered = Invoke-Api -Method POST -Path '/api/v1/public/auth/register' -Body @{
        email = $Email; password = 'StageA-passw0rd'; nickname = $Nickname
    }
    if ($registered.Status -ne 200 -and $registered.Status -ne 201) {
        throw "registering $Email failed with status $($registered.Status): $($registered.Raw)"
    }
    $loggedIn = Invoke-Api -Method POST -Path '/api/v1/public/auth/login' -Session $session -Body @{
        email = $Email; password = 'StageA-passw0rd'
    }
    if ($loggedIn.Status -ne 200) {
        throw "logging in $Email failed with status $($loggedIn.Status): $($loggedIn.Raw)"
    }

    # The session cookies are path-scoped (/api/v1/protected for the access token,
    # / for the CSRF token). Collect them from the login response headers AND from
    # the session container, then re-add every one at the host root, so the
    # requests this gate sends carry the session the login actually issued no
    # matter which edition produced the response.
    $cookies = Get-SessionCookieTable -Values $loggedIn.SetCookie -Session $session
    $missing = @()
    foreach ($required in @('pp_user_at', 'pp_user_csrf')) {
        if (-not $cookies.ContainsKey($required)) { $missing += $required }
    }
    if ($missing.Count -gt 0) {
        $headerCount = @($loggedIn.SetCookie).Count
        $containerNames = @()
        foreach ($path in @('/api/v1/protected', '/')) {
            $containerNames += @($session.Cookies.GetCookies([uri]($baseURI + $path)) | ForEach-Object { $_.Name })
        }
        throw ("login for $Email did not issue $($missing -join ', '); " +
            "set-cookie headers=$headerCount container=[$($containerNames -join ',')] parsed=[$($cookies.Keys -join ',')]")
    }
    $session.Cookies = New-Object System.Net.CookieContainer
    foreach ($name in $cookies.Keys) {
        $cookie = New-Object System.Net.Cookie($name, $cookies[$name], '/', '127.0.0.1')
        $session.Cookies.Add($cookie)
    }
    return [pscustomobject]@{
        Session = $session
        CSRF    = $cookies['pp_user_csrf']
        Email   = $Email
    }
}

function Invoke-Protected {
    param(
        [Parameter(Mandatory)]$Client,
        [Parameter(Mandatory)][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        $Body
    )
    return Invoke-Api -Method $Method -Path $Path -Session $Client.Session -Body $Body -Headers @{
        'X-CSRF-Token' = $Client.CSRF
    }
}

function Wait-ForIndexedTotal {
    param(
        [Parameter(Mandatory)]$Client,
        [Parameter(Mandatory)][string]$Keyword,
        [Parameter(Mandatory)][int]$ExpectedTotal,
        [Parameter(Mandatory)][int]$TimeoutSeconds
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $last = -1
    while ((Get-Date) -lt $deadline) {
        $page = Invoke-Protected -Client $Client -Method GET -Path "/api/v1/protected/documents/search?keyword=$([uri]::EscapeDataString($Keyword))&page=1&pageSize=1"
        if ($page.Status -eq 200) {
            $last = [int]$page.Body.meta.total
            if ($last -eq $ExpectedTotal) { return $last }
        }
        else {
            # A denial is reported as an empty page, so any other status is an
            # infrastructure failure worth surfacing rather than polling past.
            throw "search poll failed with status $($page.Status): $($page.Raw)"
        }
        Start-Sleep -Milliseconds 500
    }
    return $last
}

function Wait-ForSearchHitVisibility {
    param(
        [Parameter(Mandatory)]$Client,
        [Parameter(Mandatory)][string]$Keyword,
        [Parameter(Mandatory)][string]$DocumentID,
        [Parameter(Mandatory)][string]$Expected,
        [Parameter(Mandatory)][int]$TimeoutSeconds
    )
    # The search index is derived data: a policy change reaches it through the
    # event stream, so a hit read immediately after the change can still describe
    # the previous access state. Polling with an explicit deadline is what makes
    # the assertion about the index rather than about the race.
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $last = '<none>'
    while ((Get-Date) -lt $deadline) {
        $page = Invoke-Protected -Client $Client -Method GET -Path "/api/v1/protected/documents/search?keyword=$([uri]::EscapeDataString($Keyword))&page=1&pageSize=100"
        if ($page.Status -eq 200) {
            $hit = @($page.Body.data.documentList | Where-Object { [string]$_.documentId -eq $DocumentID })
            if ($hit.Count -eq 1) {
                $last = [string]$hit[0].visibility
                if ($last -eq $Expected) { return $last }
            }
            else {
                $last = "absent($($hit.Count))"
            }
        }
        Start-Sleep -Milliseconds 500
    }
    return $last
}

# --- self test ---------------------------------------------------------------

# Test-CookieParsing proves the cookie reader on the two shapes the editions
# actually produce, without needing both editions installed:
#
#   * PowerShell 7 hands Invoke-WebRequest a System.Net.Http.HttpResponseHeaders,
#     whose Set-Cookie indexer yields separate values. This test builds that real
#     .NET type with three Set-Cookie headers and parses it.
#   * Windows PowerShell 5.1 hands back a WebHeaderCollection whose indexer joins
#     every Set-Cookie header into one string. That is exercised as a joined
#     string, and includes an Expires attribute whose comma must NOT be taken for
#     a header boundary.
#
# Run with: .\deployments\verify-stage-a-web.ps1 -SelfTest
function Test-CookieParsing {
    $failures = 0
    $access = 'pp_user_at=eyJhbGciOiJSUzI1NiJ9.payload.signature; Path=/api/v1/protected; HttpOnly; SameSite=Lax'
    $refresh = 'pp_user_rt=refresh-token-value; Path=/api/v1/public/auth/refresh; HttpOnly; Expires=Wed, 01 Oct 2026 00:00:00 GMT'
    $csrf = 'pp_user_csrf=nonce.mac; Path=/; SameSite=Lax'

    # Shape 1: PowerShell 7's HttpResponseHeaders with three separate values.
    try {
        Add-Type -AssemblyName System.Net.Http -ErrorAction Stop
        $message = New-Object System.Net.Http.HttpResponseMessage
        $null = $message.Headers.TryAddWithoutValidation('Set-Cookie', $access)
        $null = $message.Headers.TryAddWithoutValidation('Set-Cookie', $refresh)
        $null = $message.Headers.TryAddWithoutValidation('Set-Cookie', $csrf)
        $parsed = ConvertTo-CookieTable (Read-SetCookieValues $message.Headers)
        $ok = $parsed.ContainsKey('pp_user_at') -and $parsed.ContainsKey('pp_user_rt') -and $parsed.ContainsKey('pp_user_csrf') -and
            $parsed['pp_user_csrf'] -eq 'nonce.mac' -and $parsed['pp_user_rt'] -eq 'refresh-token-value'
        Write-Host ("  {0}  HttpResponseHeaders (PowerShell 7 shape): parsed=[{1}]" -f $(if ($ok) { 'PASS' } else { 'FAIL' }), ($parsed.Keys -join ','))
        if (-not $ok) { $failures++ }
    }
    catch {
        Write-Host "  FAIL  HttpResponseHeaders shape could not be exercised: $($_.Exception.Message)"
        $failures++
    }

    # Shape 2: Windows PowerShell 5.1's joined header string.
    $joined = "$access, $refresh, $csrf"
    $parsedJoined = ConvertTo-CookieTable (Read-SetCookieValues $joined)
    $okJoined = $parsedJoined.ContainsKey('pp_user_at') -and $parsedJoined.ContainsKey('pp_user_rt') -and $parsedJoined.ContainsKey('pp_user_csrf') -and
        $parsedJoined['pp_user_csrf'] -eq 'nonce.mac'
    Write-Host ("  {0}  joined header string (Windows PowerShell 5.1 shape): parsed=[{1}]" -f $(if ($okJoined) { 'PASS' } else { 'FAIL' }), ($parsedJoined.Keys -join ','))
    if (-not $okJoined) { $failures++ }

    # Shape 3: an array of separate values, which is what GetValues returns when
    # the host exposes one and what a future edition may prefer.
    $parsedArray = ConvertTo-CookieTable (Read-SetCookieValues @($access, $refresh, $csrf))
    $okArray = $parsedArray.Count -eq 3 -and $parsedArray['pp_user_at'] -like 'eyJhbGciOiJSUzI1NiJ9*'
    Write-Host ("  {0}  separate header values: parsed=[{1}]" -f $(if ($okArray) { 'PASS' } else { 'FAIL' }), ($parsedArray.Keys -join ','))
    if (-not $okArray) { $failures++ }

    if ($failures -gt 0) {
        Write-Host "COOKIE_PARSING=FAIL"
        return $false
    }
    Write-Host "COOKIE_PARSING=PASS"
    return $true
}

if ($SelfTest) {
    if (Test-CookieParsing) { exit 0 }
    exit 1
}

# --- run ---------------------------------------------------------------------

New-Item -ItemType Directory -Path $workDirectory -Force | Out-Null
Write-Host "run id: $runID"
Write-Host "artifact directory: $workDirectory"

$documentServiceDsn = "postgres://document_service_writer:document_service@${PostgresHost}:${PostgresPort}/${Database}?sslmode=disable"
$documentSearchDsn = "postgres://document_search_writer:document_search@${PostgresHost}:${PostgresPort}/${Database}?sslmode=disable"

$documentService = $null
$documentSearch = $null
$backend = $null

try {
    if ($UseRunningStack) {
        Write-Step "Use the already running stack at $baseURI"
        $probe = Invoke-Api -Method GET -Path '/readyz'
        if ($probe.Status -ne 200) {
            throw "the running stack at $baseURI is not ready (status $($probe.Status)); start it first, for example with docker compose up -d --wait"
        }
        Write-Host "  ready: $($probe.Raw)"
    }
    else {
        Write-Step 'Build and start document-service'
        $documentService = New-ServiceProcess -Name 'document-service' -ModuleDirectory 'apps/document-service' -Binary 'cmd/document-service' -ProbeUrl "http://127.0.0.1:$DocumentServiceHTTPPort/readyz" -Environment @{
            DOCUMENT_SERVICE_POSTGRES_DSN        = $documentServiceDsn
            DOCUMENT_SERVICE_CAPABILITY_KEY_FILE = $BoundaryKeyFile
            DOCUMENT_SERVICE_GRPC_LISTEN_ADDRESS = "127.0.0.1:$DocumentServiceGRPCPort"
            DOCUMENT_SERVICE_HTTP_LISTEN_ADDRESS = "127.0.0.1:$DocumentServiceHTTPPort"
            DOCUMENT_SERVICE_ENABLE_REFLECTION    = 'false'
        }
        Wait-Probe -Service $documentService -TimeoutSeconds $ReadyTimeoutSeconds

        Write-Step 'Build and start document-search'
        $documentSearch = New-ServiceProcess -Name 'document-search' -ModuleDirectory 'apps/document-search' -Binary 'cmd/document-search' -ProbeUrl "http://127.0.0.1:$DocumentSearchHTTPPort/readyz" -Environment @{
            DOCUMENT_SEARCH_POSTGRES_DSN               = $documentSearchDsn
            DOCUMENT_SEARCH_CAPABILITY_KEY_FILE        = $BoundaryKeyFile
            DOCUMENT_SEARCH_SOURCE_CAPABILITY_KEY_FILE = $BoundaryKeyFile
            DOCUMENT_SEARCH_GRPC_LISTEN_ADDRESS        = "127.0.0.1:$DocumentSearchGRPCPort"
            DOCUMENT_SEARCH_HTTP_LISTEN_ADDRESS        = "127.0.0.1:$DocumentSearchHTTPPort"
            DOCUMENT_SEARCH_SOURCE_ENDPOINT            = "127.0.0.1:$DocumentServiceGRPCPort"
            DOCUMENT_SEARCH_SOURCE_CALLER_IDENTITY     = 'document-service'
            DOCUMENT_SEARCH_SOURCE_AUDIENCE            = 'document-service'
            DOCUMENT_SEARCH_SOURCE_SCOPE               = 'document-event-reader'
        }
        Wait-Probe -Service $documentSearch -TimeoutSeconds $ReadyTimeoutSeconds

        Write-Step 'Write the gin-backend acceptance configuration'
    $backendConfig = Join-Path $workDirectory 'config.yaml'
    $privateKey = Join-Path $repositoryRoot 'apps/gin-backend/configs/rsa_private.pem'
    $publicKey = Join-Path $repositoryRoot 'apps/gin-backend/configs/rsa_public.pem'
    foreach ($required in @($privateKey, $publicKey)) {
        if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
            throw "JWT key is missing: $required (run deployments/bootstrap.ps1)"
        }
    }
    $configText = @"
server:
  port: $BackendPort
  # Debug mode deliberately: release mode requires secure cookies, and this gate
  # talks plain HTTP to a loopback address. The mode changes logging verbosity,
  # not the routes or the document behaviour under test.
  mode: debug
  read_timeout: 30s
  write_timeout: 30s

postgres:
  host: $PostgresHost
  port: $PostgresPort
  # The Web account, not the bootstrap superuser: the gate must run the surface
  # the way it runs in deployment, where go-web has no privilege on the
  # source-owned service schemas.
  user: go_web_app
  password: "go_web_app"
  dbname: $Database
  max_idle_conns: 5
  max_open_conns: 20

redis:
  host: 127.0.0.1
  port: $RedisPort
  password: ""
  db: 0

source_owned_services:
  enabled: true
  document_service_address: "127.0.0.1:$DocumentServiceGRPCPort"
  document_search_address: "127.0.0.1:$DocumentSearchGRPCPort"
  document_service_audience: document-service
  document_search_audience: document-search
  capability_audience: document-search
  caller: go-web
  actor: go-web-api
  capability_key_path: "$($BoundaryKeyFile -replace '\\', '/')"
  request_timeout: 15s

custom:
  jwt:
    issuer: "gin-backend"
    private_key_path: "$($privateKey -replace '\\', '/')"
    public_key_path: "$($publicKey -replace '\\', '/')"
    access_expire_hours: 24
    refresh_expire_hours: 168
  cookie:
    secure: false
    domain: ""
    same_site: lax
    csrf_secret: "stage-a-acceptance-secret-0123456789abcdef"
  cors:
    allow_origins:
      - "http://localhost:5173"
    allow_methods: [GET, POST, PUT, DELETE, OPTIONS]
    allow_headers: [Origin, Content-Type, X-CSRF-Token]
    expose_headers: [Content-Length]
    allow_credentials: true
    max_age: 12h
  upload:
    max_size: 10485760
    allow_exts: [.png]
    path: "$($workDirectory -replace '\\', '/')/uploads"

log:
  level: info
  format: text
  output: stdout
  file_path: "$($workDirectory -replace '\\', '/')/app.log"
"@
    Set-Content -LiteralPath $backendConfig -Value $configText -Encoding UTF8 -NoNewline

    Write-Step 'Build and start gin-backend'
    $backend = New-ServiceProcess -Name 'gin-backend' -ModuleDirectory 'apps/gin-backend' -Binary 'cmd/server' -ProbeUrl "$baseURI/readyz" -Environment @{
        GIN_CONFIG_PATH = $backendConfig
    }
    Wait-Probe -Service $backend -TimeoutSeconds $ReadyTimeoutSeconds
    }

    Write-Step 'Register and authenticate two Web subjects'
    $clientA = New-Session -Email "stage-a-$runID-a@example.com" -Nickname "stagea$($runID)a"
    $clientB = New-Session -Email "stage-a-$runID-b@example.com" -Nickname "stagea$($runID)b"
    Write-Host "  subject A: $($clientA.Email)"
    Write-Host "  subject B: $($clientB.Email)"

    $sharedMarker = "stagemarker$runID"
    $privateMarker = "stageprivatetoken$runID"

    Write-Step "Create $DocumentCount documents for subject A"
    $createdIDs = New-Object System.Collections.Generic.List[string]
    for ($i = 1; $i -le $DocumentCount; $i++) {
        $created = Invoke-Protected -Client $clientA -Method POST -Path '/api/v1/protected/documents' -Body @{
            title   = "A document $i"
            content = "# A document $i`n$sharedMarker body $i"
            visibility = 'private'
        }
        if ($created.Status -ne 201) {
            throw "creating document $i failed with status $($created.Status): $($created.Raw)"
        }
        $createdIDs.Add([string]$created.Body.data.documentId)
    }
    Assert-That -Condition ($createdIDs.Count -eq $DocumentCount) -Description "created $DocumentCount documents"
    Assert-That -Condition (($createdIDs | Select-Object -Unique).Count -eq $DocumentCount) -Description 'every created document has a distinct id'

    Write-Step "Cursor pagination: $DocumentCount documents at page size $PageSize"
    $seen = New-Object System.Collections.Generic.List[string]
    $pageSizes = New-Object System.Collections.Generic.List[int]
    $cursor = ''
    $total = -1
    $guard = 0
    while ($true) {
        $guard++
        if ($guard -gt 20) { throw 'cursor pagination did not terminate' }
        $path = "/api/v1/protected/documents/mine?pageSize=$PageSize"
        if ($cursor) { $path += "&cursor=$([uri]::EscapeDataString($cursor))" }
        $page = Invoke-Protected -Client $clientA -Method GET -Path $path
        if ($page.Status -ne 200) { throw "listing failed with status $($page.Status): $($page.Raw)" }
        $items = @($page.Body.data.documentList)
        $pageSizes.Add($items.Count)
        foreach ($item in $items) { $seen.Add([string]$item.documentId) }
        if ($total -lt 0) { $total = [int]$page.Body.meta.total }
        $cursor = [string]$page.Body.meta.nextCursor
        if (-not $cursor -or $items.Count -eq 0) { break }
    }
    $expectedPageSizes = @()
    $remaining = $DocumentCount
    while ($remaining -gt 0) {
        $expectedPageSizes += [Math]::Min($PageSize, $remaining)
        $remaining -= $PageSize
    }
    Write-Host "  page sizes observed: $($pageSizes -join ', ')"
    Assert-That -Condition (($pageSizes -join ',') -eq ($expectedPageSizes -join ',')) -Description "pages read as $($expectedPageSizes -join '/')"
    Assert-That -Condition ($total -eq $DocumentCount) -Description "meta.total reports the real total ($total)"
    Assert-That -Condition ($seen.Count -eq $DocumentCount) -Description 'pagination returned no duplicate row'
    Assert-That -Condition (($seen | Select-Object -Unique).Count -eq $DocumentCount) -Description 'pagination covered every document exactly once'
    $seenSet = @{}
    foreach ($id in $seen) { $seenSet[$id] = $true }
    $missing = @($createdIDs | Where-Object { -not $seenSet.ContainsKey($_) })
    Assert-That -Condition ($missing.Count -eq 0) -Description 'no created document was skipped by pagination'

    Write-Step 'Subject isolation: public and private documents'
    $bPublic = Invoke-Protected -Client $clientB -Method POST -Path '/api/v1/protected/documents' -Body @{
        title = 'B public document'; content = "B public body $sharedMarker"; visibility = 'public'
    }
    if ($bPublic.Status -ne 201) { throw "creating B's public document failed: $($bPublic.Raw)" }
    $bPublicID = [string]$bPublic.Body.data.documentId

    $bPrivate = Invoke-Protected -Client $clientB -Method POST -Path '/api/v1/protected/documents' -Body @{
        title = 'B private document'; content = "B private body $privateMarker"; visibility = 'private'
    }
    if ($bPrivate.Status -ne 201) { throw "creating B's private document failed: $($bPrivate.Raw)" }
    $bPrivateID = [string]$bPrivate.Body.data.documentId

    $aMine = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/mine?pageSize=100"
    $aMineIDs = @($aMine.Body.data.documentList | ForEach-Object { [string]$_.documentId })
    Assert-That -Condition ($aMineIDs -notcontains $bPrivateID) -Description "A's own list excludes B's private document"
    Assert-That -Condition ($aMineIDs -notcontains $bPublicID) -Description "A's own list excludes B's public document"

    $aPublic = Invoke-Protected -Client $clientA -Method GET -Path '/api/v1/protected/documents/public?pageSize=100'
    $aPublicIDs = @($aPublic.Body.data.documentList | ForEach-Object { [string]$_.documentId })
    Assert-That -Condition ($aPublicIDs -contains $bPublicID) -Description "A's public list contains B's public document"
    Assert-That -Condition ($aPublicIDs -notcontains $bPrivateID) -Description "A's public list excludes B's private document"

    $readBPublic = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$bPublicID"
    Assert-That -Condition ($readBPublic.Status -eq 200) -Description 'A may read a public document owned by B'
    $readBPrivate = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$bPrivateID"
    Assert-That -Condition ($readBPrivate.Status -eq 403) -Description "A is refused B's private document with 403 (got $($readBPrivate.Status))"

    Write-Step 'Save returns the new body immediately'
    $targetID = $createdIDs[0]
    $newBody = "# saved $runID`n$sharedMarker saved body"
    $saved = Invoke-Protected -Client $clientA -Method PUT -Path "/api/v1/protected/documents/$targetID" -Body @{
        title = 'A document 1 saved'; content = $newBody; visibility = 'private'
    }
    Assert-That -Condition ($saved.Status -eq 200) -Description "saving succeeded (status $($saved.Status))"
    Assert-That -Condition ([string]$saved.Body.data.content -eq $newBody) -Description 'save response carries the new body'
    $afterSave = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$targetID"
    Assert-That -Condition ([string]$afterSave.Body.data.content -eq $newBody) -Description 'detail read after save returns the new body'

    Write-Step 'Non-owner and stale-revision refusals'
    $foreignSave = Invoke-Protected -Client $clientB -Method PUT -Path "/api/v1/protected/documents/$targetID" -Body @{
        title = 'hijack'; content = 'hijack'; visibility = 'public'
    }
    Assert-That -Condition ($foreignSave.Status -eq 403) -Description "a non-owner save is refused with 403 (got $($foreignSave.Status))"
    $afterForeign = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$targetID"
    Assert-That -Condition ([string]$afterForeign.Body.data.content -eq $newBody) -Description 'the refused save changed nothing'

    $stale = Invoke-Protected -Client $clientA -Method PUT -Path "/api/v1/protected/documents/$targetID" -Body @{
        title = 'stale'; content = 'stale body'; visibility = 'private'; expectedRevision = 1
    }
    Assert-That -Condition ($stale.Status -eq 409) -Description "a stale expected revision is refused with 409 (got $($stale.Status))"
    $afterStale = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$targetID"
    Assert-That -Condition ([string]$afterStale.Body.data.content -eq $newBody) -Description 'the stale save changed nothing'

    Write-Step 'Access policy change is visible in detail and list'
    $toPublic = Invoke-Protected -Client $clientA -Method PUT -Path "/api/v1/protected/documents/$targetID" -Body @{
        title = 'A document 1 saved'; content = $newBody; visibility = 'public'
    }
    Assert-That -Condition ($toPublic.Status -eq 200) -Description "switching to public succeeded (status $($toPublic.Status))"
    Assert-That -Condition ([string]$toPublic.Body.data.visibility -eq 'public') -Description 'save response reports public'
    $detailPublic = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$targetID"
    Assert-That -Condition ([string]$detailPublic.Body.data.visibility -eq 'public') -Description 'detail read reports public'

    $mineAfterPublic = Invoke-Protected -Client $clientA -Method GET -Path '/api/v1/protected/documents/mine?pageSize=100'
    $mineSummaryPublic = @($mineAfterPublic.Body.data.documentList | Where-Object { [string]$_.documentId -eq $targetID })
    Assert-That -Condition ($mineSummaryPublic.Count -eq 1 -and [string]$mineSummaryPublic[0].visibility -eq 'public') -Description 'list summary reports the same public state as the detail read'

    $toPrivate = Invoke-Protected -Client $clientA -Method PUT -Path "/api/v1/protected/documents/$targetID" -Body @{
        title = 'A document 1 saved'; content = $newBody; visibility = 'private'
    }
    Assert-That -Condition ([string]$toPrivate.Body.data.visibility -eq 'private') -Description 'switching back to private is applied'
    $detailPrivate = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$targetID"
    Assert-That -Condition ([string]$detailPrivate.Body.data.visibility -eq 'private') -Description 'detail read agrees after switching back'

    Write-Step 'Search: scoped totals, paging and owner isolation'
    $expectedIndexed = $DocumentCount
    $indexed = Wait-ForIndexedTotal -Client $clientA -Keyword $sharedMarker -ExpectedTotal $expectedIndexed -TimeoutSeconds $IndexTimeoutSeconds
    Assert-That -Condition ($indexed -eq $expectedIndexed) -Description "search total reached $expectedIndexed (observed $indexed); B's public document is excluded by the owner filter"

    $searchFirst = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/search?keyword=$([uri]::EscapeDataString($sharedMarker))&page=1&pageSize=$PageSize"
    Assert-That -Condition ($searchFirst.Status -eq 200) -Description "search succeeded (status $($searchFirst.Status))"
    Assert-That -Condition ([int]$searchFirst.Body.meta.total -eq $expectedIndexed) -Description "search meta.total is the real total ($($searchFirst.Body.meta.total))"
    Assert-That -Condition (@($searchFirst.Body.data.documentList).Count -eq $PageSize) -Description "search page 1 returns $PageSize hits"
    $searchFirstIDs = @($searchFirst.Body.data.documentList | ForEach-Object { [string]$_.documentId })
    Assert-That -Condition ($searchFirstIDs -notcontains $bPublicID) -Description "personal search excludes B's public document"

    $searchSecond = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/search?keyword=$([uri]::EscapeDataString($sharedMarker))&page=2&pageSize=$PageSize"
    $searchSecondIDs = @($searchSecond.Body.data.documentList | ForEach-Object { [string]$_.documentId })
    $overlap = @($searchFirstIDs | Where-Object { $searchSecondIDs -contains $_ })
    Assert-That -Condition ($overlap.Count -eq 0) -Description 'search page 1 and page 2 are disjoint'

    $searchThird = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/search?keyword=$([uri]::EscapeDataString($sharedMarker))&page=3&pageSize=$PageSize"
    $searchThirdIDs = @($searchThird.Body.data.documentList | ForEach-Object { [string]$_.documentId })
    Assert-That -Condition ($searchThirdIDs.Count -eq ($expectedIndexed - 2 * $PageSize)) -Description "search page 3 returns the remaining $($expectedIndexed - 2 * $PageSize) hits (got $($searchThirdIDs.Count))"

    $searchWide = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/search?keyword=$([uri]::EscapeDataString($sharedMarker))&page=1&pageSize=100"
    $wideIDs = @($searchWide.Body.data.documentList | ForEach-Object { [string]$_.documentId })
    Assert-That -Condition ($wideIDs.Count -eq $expectedIndexed) -Description "a single wide search page returns every owned match ($($wideIDs.Count))"
    Assert-That -Condition ($wideIDs -notcontains $bPublicID) -Description "a wide personal search still excludes B's public document"
    $targetHit = @($searchWide.Body.data.documentList | Where-Object { [string]$_.documentId -eq $targetID })
    Assert-That -Condition ($targetHit.Count -eq 1) -Description 'the searched document is present in the index'
    $hitVisibility = Wait-ForSearchHitVisibility -Client $clientA -Keyword $sharedMarker -DocumentID $targetID -Expected 'private' -TimeoutSeconds $IndexTimeoutSeconds
    Assert-That -Condition ($hitVisibility -eq 'private') -Description "the search hit converges on the real access state (observed $hitVisibility)"
    if ($targetHit.Count -eq 1) {
        Assert-That -Condition ([int64]$targetHit[0].createdAt -gt 0 -and [int64]$targetHit[0].updatedAt -gt 0) -Description 'the search hit carries real timestamps'
    }

    Write-Step 'Delete removes the document from the active list and from search'
    $deleted = Invoke-Protected -Client $clientA -Method DELETE -Path "/api/v1/protected/documents/$targetID"
    Assert-That -Condition ($deleted.Status -eq 200) -Description "delete succeeded (status $($deleted.Status))"
    # Trash is a lifecycle transition, not a physical delete: the owner can still
    # read the document, but it leaves the active listing and the index.
    $afterDelete = Invoke-Protected -Client $clientA -Method GET -Path "/api/v1/protected/documents/$targetID"
    Assert-That -Condition ($afterDelete.Status -eq 200) -Description "the owner can still read the trashed document (got $($afterDelete.Status))"
    $mineAfterDelete = Invoke-Protected -Client $clientA -Method GET -Path '/api/v1/protected/documents/mine?pageSize=100'
    $mineAfterDeleteIDs = @($mineAfterDelete.Body.data.documentList | ForEach-Object { [string]$_.documentId })
    Assert-That -Condition ($mineAfterDeleteIDs -notcontains $targetID) -Description 'the deleted document left the active listing'
    Assert-That -Condition ([int]$mineAfterDelete.Body.meta.total -eq ($DocumentCount - 1)) -Description "the active listing total dropped to $($DocumentCount - 1) (got $($mineAfterDelete.Body.meta.total))"
    $indexedAfterDelete = Wait-ForIndexedTotal -Client $clientA -Keyword $sharedMarker -ExpectedTotal ($expectedIndexed - 1) -TimeoutSeconds $IndexTimeoutSeconds
    Assert-That -Condition ($indexedAfterDelete -eq ($expectedIndexed - 1)) -Description "search total dropped to $($expectedIndexed - 1) after delete (observed $indexedAfterDelete)"
}
finally {
    if (-not $KeepRunning) {
        Write-Step 'Stop the acceptance services'
        Stop-ServiceProcess -Service $backend
        Stop-ServiceProcess -Service $documentSearch
        Stop-ServiceProcess -Service $documentService
    }
    else {
        Write-Host "`n-KeepRunning was passed; services are still up. Logs: $workDirectory"
    }
}

Write-Host ""
Write-Host "assertions: $assertionCount, failures: $failureCount"
if ($failureCount -gt 0) {
    Write-Host "STAGE_A_WEB=FAIL"
    exit 1
}
Write-Host "STAGE_A_WEB=PASS"
Write-Host "logs: $workDirectory"
