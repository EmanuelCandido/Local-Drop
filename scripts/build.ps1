$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'build-go.ps1')
if ($LASTEXITCODE -ne 0) {
    throw "Build do LocalDrop Go falhou com código $LASTEXITCODE."
}
