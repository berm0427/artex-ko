@echo off
REM Serve the v34 adapter (stays running). Close this window or Ctrl+C to stop.
REM   health: http://127.0.0.1:12354/health    api: http://127.0.0.1:12354/v1
wsl.exe -e bash -lc "bash /mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko/learning/winrun/serve-v34.sh"
