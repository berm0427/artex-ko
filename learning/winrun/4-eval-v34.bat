@echo off
REM Serve v34 + score the holdout + tear down, all in one. Writes
REM learning\eval_v33_candidate_v34.jsonl
wsl.exe -e bash -lc "bash /mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko/learning/winrun/eval-v34.sh"
echo.
pause
