$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$logDirectory = Join-Path $projectRoot 'logs'
New-Item -ItemType Directory -Path $logDirectory -Force | Out-Null
$portableGo = Join-Path $projectRoot '.tools\go\bin\go.exe'
$go = if (Test-Path -LiteralPath $portableGo) { $portableGo } else { (Get-Command go -ErrorAction Stop).Source }
$env:LOCALDROP_AUTO_OPEN = 'false'
$env:GOCACHE = Join-Path $projectRoot 'go-backend\.cache\go-build'
$env:GOMODCACHE = Join-Path $projectRoot 'go-backend\.cache\go-mod'

$backend = Start-Process `
    -FilePath $go `
    -ArgumentList 'run','.','--headless' `
    -WorkingDirectory (Join-Path $projectRoot 'go-backend') `
    -WindowStyle Hidden `
    -RedirectStandardOutput (Join-Path $logDirectory 'backend-dev.log') `
    -RedirectStandardError (Join-Path $logDirectory 'backend-dev-error.log') `
    -PassThru

try {
    Push-Location (Join-Path $projectRoot 'frontend')
    Write-Host 'Frontend: http://localhost:5173' -ForegroundColor Cyan
    Write-Host 'Backend:  http://localhost:8088' -ForegroundColor Cyan
    & npm.cmd run dev
} finally {
    Pop-Location
    if (!$backend.HasExited) {
        Stop-Process -Id $backend.Id
    }
}
