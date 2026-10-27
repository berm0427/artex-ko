#!/usr/bin/env bash
# Regenerate the agentic training set (v2) and the Google-CTF holdout (v33).
# Deterministic, stdlib only. Safe to re-run.
set -euo pipefail
REPO=/mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko
PY=/home/c0d1n9/.venvs/artex-qlora-check/bin/python
# prepare_* resolve their data paths relative to CWD, so run from learning/.
cd "$REPO/learning"
"$PY" prepare_agentic_sft_v2.py
"$PY" prepare_agentic_holdout_v33.py
echo "GEN_DONE"
echo "  train   = learning/agentic_sft_v2/train.jsonl (+ manifest.json)"
echo "  holdout = learning/agentic_holdout_v33.jsonl (+ _manifest.json)"
