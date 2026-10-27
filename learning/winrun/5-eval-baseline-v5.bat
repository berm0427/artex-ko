@echo off
REM Baseline comparison on the same holdout. Writes
REM learning\eval_v33_baseline_v5.jsonl
wsl.exe -e bash -lc "bash /mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko/learning/winrun/eval-baseline-v5.sh"
echo.
pause
