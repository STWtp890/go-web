[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$tempBase = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$tempRoot = Join-Path $tempBase ('go-web-proto-verify-' + [guid]::NewGuid().ToString('N'))
$tempGenerated = Join-Path $tempRoot 'generated'

# Every committed contract and the generated files it must produce. The relative
# path is identical on both sides: --go_opt=module=packages/gen strips the module
# prefix, so a fresh run writes <out>/<relative>, matching packages/gen/<relative>.
$targets = @(
    @{
        Proto = 'packages/proto/mixin-search/v1/mixin-search.proto'
        Files = @(
            'mixin-search/v1/mixin-search.pb.go',
            'mixin-search/v1/mixin-search_grpc.pb.go'
        )
    },
    @{
        Proto = 'packages/proto/mixin-search/chat/v1/chat.proto'
        Files = @(
            'mixin-search/chat/v1/chat.pb.go',
            'mixin-search/chat/v1/chat_grpc.pb.go'
        )
    }
)

foreach ($command in @('protoc', 'protoc-gen-go', 'protoc-gen-go-grpc')) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "$command is required to verify generated RPC code"
    }
}

New-Item -ItemType Directory -Path $tempGenerated | Out-Null
try {
    foreach ($target in $targets) {
        Push-Location $repositoryRoot
        try {
            $protocArguments = @(
                '-I', $repositoryRoot,
                "--go_out=$tempGenerated",
                '--go_opt=module=packages/gen',
                "--go-grpc_out=$tempGenerated",
                '--go-grpc_opt=module=packages/gen',
                $target.Proto
            )
            & protoc @protocArguments
            if ($LASTEXITCODE -ne 0) {
                throw "protoc failed for $($target.Proto) with exit code $LASTEXITCODE"
            }
        }
        finally {
            Pop-Location
        }

        foreach ($relativePath in $target.Files) {
            $expected = Join-Path (Join-Path $repositoryRoot 'packages/gen') $relativePath
            $actual = Join-Path $tempGenerated $relativePath
            if (-not (Test-Path -LiteralPath $expected)) {
                throw "generated file is missing: $expected"
            }
            if (-not (Test-Path -LiteralPath $actual)) {
                throw "protoc did not produce the expected file for $($target.Proto): $relativePath"
            }

            $expectedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $expected).Hash
            $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $actual).Hash
            if ($expectedHash -ne $actualHash) {
                throw "$relativePath is stale; regenerate packages/gen from $($target.Proto)"
            }
        }

        Write-Host "PASS generated Go code matches $($target.Proto)."
    }
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
