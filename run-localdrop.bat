@echo off
setlocal
set "APP=%~dp0dist\LocalDrop.exe"
if not exist "%APP%" (
  echo dist\LocalDrop.exe nao existe. Execute scripts\build.ps1 primeiro.
  exit /b 1
)
"%APP%" %*
