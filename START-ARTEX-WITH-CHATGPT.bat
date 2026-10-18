@echo off
setlocal
cd /d "%~dp0"

if not exist "%~dp0START-ARTEX-KO.cmd" (
  echo [ARTEX-KO] START-ARTEX-KO.cmd not found. Run scripts\install-windows.ps1 first. 1>&2
  exit /b 1
)

if defined ARTEX_KO_LAUNCHER_CHECK exit /b 0

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start-chatgpt-bridge.ps1"
if %ERRORLEVEL% EQU 2 (
  echo [ARTEX-KO] ARTEX is already running. Open http://127.0.0.1:8787
  exit /b 0
)
if errorlevel 1 (
  echo [ARTEX-KO] ChatGPT bridge is not ready. ARTEX was not started. 1>&2
  exit /b 1
)

echo [ARTEX-KO] ChatGPT bridge ready. Starting ARTEX...
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\connect-codex-help.ps1"
if errorlevel 1 (
  echo [ARTEX-KO] Codex help connection failed. ARTEX was not started. 1>&2
  exit /b 1
)
set "ARTEX_KO_LAUNCHER_CHECK=1"
call "%~dp0START-ARTEX-KO.cmd"
if errorlevel 1 exit /b 1
set "ARTEX_KO_LAUNCHER_CHECK="

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\sync-chatgpt-bridge-profile.ps1"
if errorlevel 1 (
  echo [ARTEX-KO] ChatGPT bridge profile sync failed. ARTEX was not started. 1>&2
  exit /b 1
)

call "%~dp0START-ARTEX-KO.cmd"
exit /b %ERRORLEVEL%
