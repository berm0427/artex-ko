@echo off
rem Use UTF-8 for process output.
chcp 65001 >nul 2>&1
rem ARTEX daemon launcher for Windows.
rem
rem Usage:
rem   start.bat                  foreground run (Ctrl-C to stop)
rem   start.bat -addr :9000      forward arguments to artex.exe
rem
rem Run artex.exe and decide whether to restart from its exit code.
rem
rem   0      normal stop -> exit this loop
rem   75     restart requested -> relaunch immediately
rem   other  abnormal exit -> backoff and retry (up to 60 seconds)
rem
rem The selfupdate package handles download, checksum verification, and swaps.

setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BIN=artex.exe"
if not exist "%BIN%" (
	echo [artex] executable not found: %BIN% 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
"%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [artex] stopped normally
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem The next boot applies the staged update or rollback.
	echo [artex] restart requested; applying staged version...
	set /a delay=1
	goto loop
)

echo [artex] abnormal exit ^(code=!code!^), retry in !delay!s 1>&2
rem timeout fails when stdin is redirected; ping provides the delay.
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop
