[CmdletBinding()]
param(
    [switch]$NoCache,
    [switch]$UseCachedBase,
    [switch]$KeepEnvironment,
    [ValidateRange(30, 1800)]
    [int]$WaitTimeoutSeconds = 300
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$composeFile = Join-Path $repositoryRoot 'docker-compose.yaml'
$projectName = 'go-web-image-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
$composePrefix = @('compose', '-p', $projectName, '-f', $composeFile)
$started = $false

function Invoke-CheckedDocker {
    param([Parameter(Mandatory)][string[]]$Arguments)

    & docker @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker command failed with exit code $LASTEXITCODE"
    }
}

function Invoke-PostgresScalar {
    param([Parameter(Mandatory)][string]$Query)

    $value = (& docker @composePrefix exec -T postgres psql -X -At -v ON_ERROR_STOP=1 -U postgres -d gin_demo -c $Query | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "PostgreSQL verification query failed: $Query"
    }
    return $value
}

Push-Location $repositoryRoot
try {
    $buildArguments = $composePrefix + @('build')
    if (-not $UseCachedBase) {
        $buildArguments += '--pull'
    }
    if ($NoCache) {
        $buildArguments += '--no-cache'
    }
    $buildArguments += 'postgres'
    Invoke-CheckedDocker $buildArguments

    Invoke-CheckedDocker @(
        'run', '--rm', '--entrypoint', 'bash', 'gin-postgres:local', '-ceu',
        'for extension in timescaledb pg_search vector; do test -f "/usr/share/postgresql/17/extension/${extension}.control"; done'
    )

    Invoke-CheckedDocker ($composePrefix + @('config', '--quiet'))
    Invoke-CheckedDocker ($composePrefix + @('up', '-d', '--wait', '--wait-timeout', $WaitTimeoutSeconds, 'postgres'))
    $started = $true

    $extensionCount = Invoke-PostgresScalar "SELECT count(*) FROM pg_extension WHERE extname IN ('timescaledb', 'pg_search', 'vector');"
    if ($extensionCount -ne '3') {
        throw "required extension count is $extensionCount, want 3"
    }
    $documentTableCount = Invoke-PostgresScalar "SELECT count(*) FROM unnest(ARRAY['knowledge_spaces','space_members','documents','document_versions','document_access_policies','document_grants','document_search_projection']) AS relation_name WHERE to_regclass('public.' || relation_name) IS NOT NULL;"
    if ($documentTableCount -ne '7') {
        throw "document table count is $documentTableCount, want 7"
    }
    $obsoleteRelationCount = Invoke-PostgresScalar "SELECT count(*) FROM unnest(ARRAY['schema_migrations','markdowns','markdown_contents','index_outbox','document_index_states','idx_markdowns_paradedb']) AS relation_name WHERE to_regclass('public.' || relation_name) IS NOT NULL;"
    if ($obsoleteRelationCount -ne '0') {
        throw "obsolete relation count is $obsoleteRelationCount, want 0"
    }
    $bm25IndexCount = Invoke-PostgresScalar "SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname='idx_document_search_projection_bm25';"
    if ($bm25IndexCount -ne '1') {
        throw "BM25 index count is $bm25IndexCount, want 1"
    }
    $hypertableCount = Invoke-PostgresScalar "SELECT count(*) FROM timescaledb_information.hypertables WHERE hypertable_name='chat_messages';"
    if ($hypertableCount -ne '1') {
        throw "chat hypertable count is $hypertableCount, want 1"
    }
    Invoke-CheckedDocker ($composePrefix + @(
        'exec', '-T', 'postgres', 'psql', '-X', '-v', 'ON_ERROR_STOP=1',
        '-U', 'postgres', '-d', 'gin_demo', '-f', '/database/sql/plugin/bm25_only_verify.sql'
    ))

    $imageID = (& docker image inspect gin-postgres:local --format '{{.Id}}' | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $imageID) {
        throw 'failed to inspect gin-postgres:local'
    }
    Write-Host "IMAGE_ID=$imageID"
    Write-Host 'SCHEMA_BASELINE=PASS'
    Write-Host 'EXTENSIONS=timescaledb,pg_search,vector'
    Write-Host 'BM25_ONLY=PASS'
    Write-Host 'CHAT_SCHEMA_PRESENT_BUT_SERVICE_DISCONNECTED=PASS'
    Write-Host 'P1.4_POSTGRES_IMAGE=PASS'
}
finally {
    if (-not $KeepEnvironment -and $started) {
        & docker @composePrefix down --volumes --remove-orphans
    }
    Pop-Location
}