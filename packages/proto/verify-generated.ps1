[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$protoSource = 'packages/proto/mixin-search/v1/mixin-search.proto'
$generatedDirectory = Join-Path $repositoryRoot 'packages/gen/mixin-search/v1'
$tempBase = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$tempRoot = Join-Path $tempBase ('go-web-proto-verify-' + [guid]::NewGuid().ToString('N'))
$tempGenerated = Join-Path $tempRoot 'generated'

foreach ($command in @('protoc', 'protoc-gen-go', 'protoc-gen-go-grpc')) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "$command is required to verify generated RPC code"
    }
}

New-Item -ItemType Directory -Path $tempGenerated | Out-Null
try {
    Push-Location $repositoryRoot
    try {
        $protocArguments = @(
            '-I', $repositoryRoot,
            "--go_out=$tempGenerated",
            '--go_opt=module=packages/gen',
            "--go-grpc_out=$tempGenerated",
            '--go-grpc_opt=module=packages/gen',
            $protoSource
        )
        & protoc @protocArguments
        if ($LASTEXITCODE -ne 0) {
            throw "protoc failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }

    foreach ($fileName in @('mixin-search.pb.go', 'mixin-search_grpc.pb.go')) {
        $expected = Join-Path $generatedDirectory $fileName
        $actual = Join-Path $tempGenerated "mixin-search/v1/$fileName"
        if (-not (Test-Path -LiteralPath $expected)) {
            throw "generated file is missing: $expected"
        }
        if (-not (Test-Path -LiteralPath $actual)) {
            throw "protoc did not produce the expected file: $actual"
        }

        $expectedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $expected).Hash
        $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $actual).Hash
        if ($expectedHash -ne $actualHash) {
            throw "$fileName is stale; regenerate packages/gen from $protoSource"
        }
    }

    Write-Host 'PASS generated mixin-search/v1 Go code matches its proto source.'
}
finally {
    $resolvedTemp = [System.IO.Path]::GetFullPath($tempRoot)
    if (-not $resolvedTemp.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "refusing to remove unexpected temporary path: $resolvedTemp"
    }
    if (Test-Path -LiteralPath $resolvedTemp) {
        Remove-Item -LiteralPath $resolvedTemp -Recurse -Force
    }
}