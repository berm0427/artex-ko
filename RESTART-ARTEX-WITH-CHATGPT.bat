@echo off
chcp 65001 >nul 2>&1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\restart-artex-with-chatgpt.ps1"
exit /b %ERRORLEVEL%
