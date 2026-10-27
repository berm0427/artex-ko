@echo off
REM Train the v34 adapter (QLoRA). Takes several minutes on the GPU.
wsl.exe -e bash -lc "bash /mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko/learning/winrun/train-v34.sh"
echo.
pause
