@echo off
setlocal
set "SCRIPT=%~dp0install.ps1"
if not exist "%SCRIPT%" (
  echo install.ps1 not found next to install.cmd. 1>&2
  exit /b 1
)
where powershell >nul 2>nul
if errorlevel 1 (
  echo PowerShell not found. 1>&2
  exit /b 1
)
powershell -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%" %*
exit /b %ERRORLEVEL%
