[CmdletBinding()]
param(
    [switch]$SkipImageBuild,
    [ValidateRange(30, 1800)]
    [int]$WaitTimeoutSeconds = 300
)

$ErrorActionPreference = 'Stop'
$composeFile = 'docker-compose.yaml'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$previousGoCache = $env:GOCACHE
$verificationGoCache = Join-Path ([System.IO.Path]::GetTempPath()) 'go-web-build-verify-cache'

function Invoke-CheckedCommand {
    param(
        [Parameter(Mandatory)]
        [string]$Label,
        [Parameter(Mandatory)]
        [scriptblock]$Command
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
        docker compose -f $composeFile config --quiet
    }

    Push-Location 'gin-backend'
    try {
        $env:GOCACHE = $verificationGoCache
        Invoke-CheckedCommand 'Run Go tests' { go test ./... }
    }
    finally {
        Pop-Location
    }

    Push-Location 'simple-frontend'
    try {
        Invoke-CheckedCommand 'Run Vue and TypeScript checks' { npm run type-check }
        Invoke-CheckedCommand 'Build the production frontend' { npm run build }
    }
    finally {
        Pop-Location
    }

    $composeArguments = @('compose', '-f', $composeFile, 'up', '-d')
    if (-not $SkipImageBuild) {
        $composeArguments += '--build'
    }
    $composeArguments += @('--wait', '--wait-timeout', $WaitTimeoutSeconds)

    Invoke-CheckedCommand 'Deploy and wait for the Compose stack' {
        docker @composeArguments
    }

    Write-Host "`n==> Verify HTTP entry points"
    $checks = @(
        @{ Name = 'Frontend'; Uri = 'http://127.0.0.1:15173/' },
        @{ Name = 'Liveness through Nginx'; Uri = 'http://127.0.0.1:15173/healthz' },
        @{ Name = 'Readiness through Nginx'; Uri = 'http://127.0.0.1:15173/readyz' }
    )

    foreach ($check in $checks) {
        $response = Invoke-WebRequest -UseBasicParsing -Uri $check.Uri -TimeoutSec 10
        if ($response.StatusCode -ne 200) {
            throw "$($check.Name) returned HTTP $($response.StatusCode)"
        }
        Write-Host "PASS $($check.Name): HTTP $($response.StatusCode) $($check.Uri)"
    }

    Invoke-CheckedCommand 'Show final container state' {
        docker compose -f $composeFile ps
    }

    Write-Host "`nBuild, deployment, and runtime verification passed."
}
finally {
    $env:GOCACHE = $previousGoCache
    Pop-Location
}
