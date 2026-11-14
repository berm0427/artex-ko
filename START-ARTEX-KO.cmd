@echo off
chcp 65001 >nul 2>&1
setlocal

rem 유휴 안전망: 열린 목표가 있는데 활동이 멈춘 작업을 bounded(depth<=2) 후속 작업으로 자동 재접근.
set "ARTEX_AUTO_CONTINUE=1"

set "PGBIN=C:\Users\berm0\Documents\Codex\2026-09-15\new-chat\tools\postgresql-17.11\pgsql\bin"
set "PGDATA=C:\Users\berm0\Documents\Codex\2026-09-15\new-chat\work\artex-pgdata"
set "PGLOG=C:\Users\berm0\Documents\Codex\2026-09-15\new-chat\work\artex-postgres.log"

"%PGBIN%\pg_isready.exe" -h 127.0.0.1 -p 5433 -U artex -t 5 >nul 2>&1
if not errorlevel 1 goto postgres_ready
rem A PostgreSQL process may already be starting but not accepting connections yet.
"%PGBIN%\pg_ctl.exe" -D "%PGDATA%" status >nul 2>&1
if not errorlevel 1 goto postgres_wait
echo [ARTEX-KO] PostgreSQL 시작 중...
"%PGBIN%\pg_ctl.exe" -D "%PGDATA%" -l "%PGLOG%" -o "-p 5433 -h 127.0.0.1" start
if errorlevel 1 goto postgres_failed
goto postgres_ready
:postgres_wait
for /L %%I in (1,1,30) do (
  "%PGBIN%\pg_isready.exe" -h 127.0.0.1 -p 5433 -U artex -t 5 >nul 2>&1
  if not errorlevel 1 goto postgres_ready
  ping -n 2 127.0.0.1 >nul 2>&1
)
echo [ARTEX-KO] PostgreSQL is running but not accepting connections. 1>&2
goto postgres_failed
:postgres_ready
echo [ARTEX-KO] http://127.0.0.1:8787
if defined ARTEX_KO_LAUNCHER_CHECK exit /b 0
call "%~dp0start.bat" -addr :8787
exit /b %ERRORLEVEL%
:postgres_failed
echo [ARTEX-KO] PostgreSQL 시작 실패. 로그: %PGLOG% 1>&2
exit /b 1
