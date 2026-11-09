param(
    [switch]$SkipFrontend,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$goRoot = Join-Path $projectRoot 'go-backend'
$frontendRoot = Join-Path $projectRoot 'frontend'
$portableGo = Join-Path $projectRoot '.tools\go\bin\go.exe'
$go = if (Test-Path -LiteralPath $portableGo) {
    $portableGo
} else {
    (Get-Command go -ErrorAction Stop).Source
}

function Assert-LastExitCode([string]$step) {
    if ($LASTEXITCODE -ne 0) {
        throw "$step falhou com código $LASTEXITCODE."
    }
}

if (!$SkipFrontend) {
    Push-Location $frontendRoot
    try {
        if (!(Test-Path -LiteralPath (Join-Path $frontendRoot 'node_modules'))) {
            & npm.cmd ci
            Assert-LastExitCode 'instalação das dependências do frontend'
        }
        & npm.cmd run build
        Assert-LastExitCode 'build do frontend'
    } finally {
        Pop-Location
    }
}

$frontendDist = Join-Path $frontendRoot 'dist'
if (!(Test-Path -LiteralPath (Join-Path $frontendDist 'index.html'))) {
    throw 'frontend\dist\index.html não existe. Execute o build do frontend.'
}

$webRoot = Join-Path $goRoot 'web'
$resolvedGoRoot = (Resolve-Path -LiteralPath $goRoot).Path
$null = New-Item -ItemType Directory -Path $webRoot -Force
$resolvedWebRoot = (Resolve-Path -LiteralPath $webRoot).Path
if (!$resolvedWebRoot.StartsWith($resolvedGoRoot + [IO.Path]::DirectorySeparatorChar)) {
    throw "Destino web inesperado: $resolvedWebRoot"
}
Get-ChildItem -LiteralPath $resolvedWebRoot -Force | Remove-Item -Recurse -Force
Copy-Item -Path (Join-Path $frontendDist '*') -Destination $resolvedWebRoot -Recurse -Force

Push-Location $goRoot
try {
    $env:GOCACHE = Join-Path $goRoot '.cache\go-build'
    $env:GOMODCACHE = Join-Path $goRoot '.cache\go-mod'
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:GOFLAGS = '-buildvcs=false'
    New-Item -ItemType Directory -Path $env:GOCACHE,$env:GOMODCACHE -Force | Out-Null

    $goFiles = Get-ChildItem -LiteralPath $goRoot -Filter '*.go' | Select-Object -ExpandProperty FullName
    & (Join-Path (Split-Path -Parent $go) 'gofmt.exe') -w $goFiles
    Assert-LastExitCode 'formatação Go'

    if (!$SkipTests) {
        & $go test -count=1 ./...
        Assert-LastExitCode 'testes do backend Go'
        & $go vet ./...
        Assert-LastExitCode 'análise estática do backend Go'
    }

    $dist = Join-Path $projectRoot 'dist'
    New-Item -ItemType Directory -Path $dist -Force | Out-Null
    & $go build -trimpath -ldflags '-s -w -H=windowsgui' -o (Join-Path $dist 'LocalDrop.exe') .
    Assert-LastExitCode 'build do executável Go'
} finally {
    Pop-Location
}

Write-Host "LocalDrop pronto: $projectRoot\dist\LocalDrop.exe" -ForegroundColor Green
