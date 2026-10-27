@echo off
REM Persistent v34 server for ARTEX wiring (profile "ARTEX v34 C", 0.0.0.0:12362).
REM Keep this window open while ARTEX uses the v34 profile. Close it or Ctrl+C to stop.
REM Requires the admin portproxy: netsh interface portproxy add v4tov4
REM   listenaddress=127.0.0.1 listenport=12362 connectaddress=<WSL_IP> connectport=12362
wsl.exe -e bash -lc "bash /mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko/learning/winrun/serve-v34-artex.sh"
