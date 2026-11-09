$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$executable = Join-Path $projectRoot 'dist\LocalDrop.exe'
if (!(Test-Path -LiteralPath $executable)) {
    throw 'dist\LocalDrop.exe não existe. Execute .\scripts\build.ps1 primeiro.'
}
& $executable @args
