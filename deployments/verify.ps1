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

function Assert-JWTKeysPresent {
    param(
        [Parameter(Mandatory)][string]$RepositoryRoot
    )

    # The JWT key pair is not baked into the image (root .dockerignore excludes **/*.pem);
    # compose mounts it read-only into the container instead. When it is missing, Docker
    # creates a directory at the mount point and LoadKeys then fails with a confusing error,
    # so fail here first. Keep this file ASCII-only: Windows PowerShell 5.1 parses
    # BOM-less .ps1 as ANSI, and a multi-byte comment can swallow the following newline.
    $keyDirectory = Join-Path -Path $RepositoryRoot -ChildPath 'apps/gin-backend/configs'
    foreach ($keyName in @('rsa_private.pem', 'rsa_public.pem')) {
        $keyPath = Join-Path -Path $keyDirectory -ChildPath $keyName
        if (-not (Test-Path -LiteralPath $keyPath -PathType Leaf)) {
            throw "missing JWT key file: $keyPath. Run first: go run ./apps/gin-backend/cmd/tools/pemgenerator"
        }
    }
}

function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory)][scriptblock]$Command
    )

    # Native tools write progress to stderr, which is normal. Windows PowerShell 5.1 turns
    # those lines into NativeCommandError records once the script's own output is redirected
    # (for example `*> file` when CI collects logs), and with $ErrorActionPreference='Stop'
    # that terminates the run. Redirecting stderr inside the script (2>&1) does NOT help.
    # Relax the preference here and let callers judge success via $LASTEXITCODE.
    $previousErrorAction = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $Command
    }
    finally {
        $ErrorActionPreference = $previousErrorAction
    }
}

function Invoke-CheckedCommand {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][scriptblock]$Command
    )

    Write-Host "`n==> $Label"
    Invoke-NativeCommand $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Label failed with exit code $LASTEXITCODE"
    }
}

function Get-IndexDeliveryStatus {
    $raw = Invoke-NativeCommand {
        docker @composePrefix exec -T document-index-worker `
            /app/document-index-admin -config /app/configs/config.yaml status -timeout 2s
    }
    if ($LASTEXITCODE -ne 0) {
        throw "Read document index delivery status failed with exit code $LASTEXITCODE"
    }
    try {
        return (($raw -join "`n") | ConvertFrom-Json)
    }
    catch {
        throw "Invalid document index delivery status JSON: $($raw -join "`n")"
    }
}

function Wait-IndexDeliverySettled {
    param([int]$TimeoutSeconds = 60)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $status = Get-IndexDeliveryStatus
        if ([int64]$status.outbox.deadLetter -gt 0) {
            throw "Document index delivery reached dead letter: $($status | ConvertTo-Json -Compress -Depth 5)"
        }
        $unfinished = [int64]$status.outbox.pending + [int64]$status.outbox.processing + [int64]$status.outbox.retry
        if ($unfinished -eq 0) {
            return $status
        }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Document index delivery did not settle within ${TimeoutSeconds}s: $($status | ConvertTo-Json -Compress -Depth 5)"
}

function Get-ShadowSearchStatus {
    param([string]$Source = '')
    if ($Source) {
        $raw = Invoke-NativeCommand {
            docker @composePrefix exec -T document-index-worker `
                /app/document-index-admin -config /app/configs/config.yaml shadow-status -source $Source
        }
    }
    else {
        $raw = Invoke-NativeCommand {
            docker @composePrefix exec -T document-index-worker `
                /app/document-index-admin -config /app/configs/config.yaml shadow-status
        }
    }
    if ($LASTEXITCODE -ne 0) {
        throw "Read document shadow search status failed with exit code $LASTEXITCODE"
    }
    try {
        return (($raw -join "`n") | ConvertFrom-Json)
    }
    catch {
        throw "Invalid document shadow search status JSON: $($raw -join "`n")"
    }
}

function Wait-ShadowObservationIncrease {
    param(
        [int64]$PreviousTotal,
        [int]$TimeoutSeconds = 15
    )
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $status = Get-ShadowSearchStatus
        if ([int64]$status.total -gt $PreviousTotal) {
            return $status
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Document shadow search observation count did not increase within ${TimeoutSeconds}s"
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

    Assert-JWTKeysPresent -RepositoryRoot $repositoryRoot

    $composeArguments = $composePrefix + @('up', '-d')
    if (-not $SkipImageBuild) {
        $composeArguments += '--build'
    }
    $composeArguments += @('--wait', '--wait-timeout', $WaitTimeoutSeconds)
    $started = $true
    Invoke-CheckedCommand 'Build and start a fresh disposable application stack' {
        docker @composeArguments
    }

    Invoke-CheckedCommand 'Verify PostgreSQL development baseline' {
        docker @composePrefix exec -T postgres psql -X -v ON_ERROR_STOP=1 -U postgres -d gin_demo -f /database/sql/plugin/bm25_only_verify.sql
    }
    Invoke-CheckedCommand 'Verify mutable entity cache revision fencing' {
        docker @composePrefix exec -T postgres psql -X -v ON_ERROR_STOP=1 -U postgres -d gin_demo -f /database/sql/plugin/cache_revision_verify.sql
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
            go run ./cmd/tools/runtimeapitest -run-id $runID -bootstrap-manager p15_admin -bootstrap-password P1_5AdminPass234 -report-dir $reportDir
        }
    }
    finally {
        Pop-Location
    }

    Invoke-CheckedCommand 'Wait for committed Documents changes to reach the shadow index' {
        $status = Wait-IndexDeliverySettled -TimeoutSeconds 60
        if (-not $status.mixinSearch.available) {
            throw "mixin-search is unavailable after initial delivery: $($status | ConvertTo-Json -Compress -Depth 5)"
        }
        Write-Host "PASS initial shadow delivery: succeeded=$($status.outbox.succeeded) failedAttempts=$($status.outbox.failedAttempts)"
    }

    Invoke-CheckedCommand 'Reconcile current document facts and drain repair events' {
        docker @composePrefix exec -T document-index-worker `
            /app/document-index-admin -config /app/configs/config.yaml reconcile -limit 100
        if ($LASTEXITCODE -eq 0) {
            $status = Wait-IndexDeliverySettled -TimeoutSeconds 60
            Write-Host "PASS reconciliation drain: pending=$($status.outbox.pending) retry=$($status.outbox.retry)"
        }
    }

    Push-Location 'apps/gin-backend'
    try {
        $evaluationRunID = 'p25_' + (Get-Date -Format 'yyyyMMdd_HHmmss')
        $evaluationReportPath = Join-Path $reportDir "document-search-evaluation-$evaluationRunID.json"
        Invoke-CheckedCommand 'Run repeatable BM25 and mixin-search relevance evaluation' {
            go run ./cmd/document-search-eval -run-id $evaluationRunID `
                -mixin-search-address 127.0.0.1:19090 -report-dir $reportDir
        }
    }
    finally {
        Pop-Location
    }

    Invoke-CheckedCommand 'Verify shadow observations and correctness gates' {
        $evaluation = Get-Content -Raw $evaluationReportPath | ConvertFrom-Json
        if ($evaluation.queries.Count -lt 7 -or
            [int]$evaluation.correctness.permissionViolations -ne 0 -or
            [int]$evaluation.correctness.lifecycleViolations -ne 0 -or
            [int]$evaluation.correctness.activeVersionViolations -ne 0) {
            throw "P2.5 evaluation correctness gate failed: $($evaluation | ConvertTo-Json -Compress -Depth 8)"
        }
        $shadow = Get-ShadowSearchStatus -Source 'evaluation'
        if ([int64]$shadow.succeeded -lt 7 -or
            [int64]$shadow.permissionViolations -ne 0 -or
            [int64]$shadow.lifecycleViolations -ne 0 -or
            [int64]$shadow.activeVersionViolations -ne 0) {
            throw "P2.5 persisted shadow observations failed: $($shadow | ConvertTo-Json -Compress -Depth 5)"
        }
        Write-Host "PASS P2.5 evaluation: decision=$($evaluation.decision) queries=$($evaluation.queries.Count) shadowRecall=$($evaluation.shadow.meanRecallAtK)"
    }

    Invoke-CheckedCommand 'Verify mixin-search outage does not block Documents or BM25' {
        $shadowBeforeOutage = Get-ShadowSearchStatus
        docker @composePrefix stop mixin-search
        if ($LASTEXITCODE -ne 0) { return }
        $ready = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8080/readyz' -TimeoutSec 10
        if ($ready.StatusCode -ne 200) {
            throw "gin-backend readiness changed during mixin-search outage: HTTP $($ready.StatusCode)"
        }
        Push-Location 'apps/gin-backend'
        try {
            $outageRunID = 'p24_outage_' + (Get-Date -Format 'yyyyMMdd_HHmmss')
            go run ./cmd/tools/runtimeapitest -run-id $outageRunID -bootstrap-manager p15_admin `
                -bootstrap-password P1_5AdminPass234 -report-dir $reportDir
        }
        finally {
            Pop-Location
        }
        $shadowAfterOutage = Wait-ShadowObservationIncrease -PreviousTotal ([int64]$shadowBeforeOutage.total)
        $failuresBefore = [int64]$shadowBeforeOutage.failed + [int64]$shadowBeforeOutage.timedOut
        $failuresAfter = [int64]$shadowAfterOutage.failed + [int64]$shadowAfterOutage.timedOut
        if ($failuresAfter -le $failuresBefore) {
            throw "mixin-search outage was not recorded by shadow query observations"
        }
        Write-Host "PASS shadow failure isolation: observations=$($shadowAfterOutage.total) failures=$failuresAfter"
    }

    Invoke-CheckedCommand 'Observe outage backlog' {
        $status = Get-IndexDeliveryStatus
        $unfinished = [int64]$status.outbox.pending + [int64]$status.outbox.processing + [int64]$status.outbox.retry
        if ($unfinished -le 0 -or $status.mixinSearch.available) {
            throw "Expected unavailable mixin-search and non-empty backlog: $($status | ConvertTo-Json -Compress -Depth 5)"
        }
        if ([int64]$status.outbox.deadLetter -ne 0) {
            throw "Retryable outage produced dead letters: $($status | ConvertTo-Json -Compress -Depth 5)"
        }
    }

    Invoke-CheckedCommand 'Restore mixin-search and automatically drain the backlog' {
        docker @composePrefix up -d --wait --wait-timeout $WaitTimeoutSeconds mixin-search document-index-worker
        if ($LASTEXITCODE -eq 0) {
            $status = Wait-IndexDeliverySettled -TimeoutSeconds 60
            if (-not $status.mixinSearch.available -or [int64]$status.outbox.deadLetter -ne 0) {
                throw "Shadow index did not recover cleanly: $($status | ConvertTo-Json -Compress -Depth 5)"
            }
            Write-Host "PASS automatic recovery: succeeded=$($status.outbox.succeeded) failedAttempts=$($status.outbox.failedAttempts)"
        }
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
    Write-Host "`nP2.5_SHADOW_QUERY_EVALUATION=PASS"
    Write-Host "P2.4_SHADOW_INDEX=PASS"
    Write-Host "P1.5_BUILD_TEST_DEPLOYMENT=PASS"
}
finally {
    $env:GOCACHE = $previousGoCache
    $env:DOCUMENT_REPOSITORY_TEST_DSN = $previousDocumentDSN
    if (-not $KeepEnvironment -and $started) {
        Invoke-NativeCommand { docker @composePrefix down --volumes --remove-orphans }
    }
    Pop-Location
}
