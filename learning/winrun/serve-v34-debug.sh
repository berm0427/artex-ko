#!/usr/bin/env bash
# v34 instrumented serve for the ARTEX long-loop redis run (condition C).
# Uses serve_openai_debug.py: SAME behavior/caps as serve_openai.py (incl. the
# stock 2MB request-body cap -> still 413 on oversize) PLUS per-POST body dumps.
# That 2MB cap is intentional here: it is the exact point #727/v32 stalled, so
# this run tests whether v34's compaction keeps request bodies under it.
#   dumps  : <priv>/bodies-v34/body_NN_tokNNNN.json
#   log    : <priv>/serve_debug_v34.jsonl
set -euo pipefail
REPO=/mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko
PY=/home/c0d1n9/.venvs/artex-qlora-check/bin/python
PRIV=/mnt/c/Users/berm0/Documents/Codex/private/artex-learning/v34-long-loop
mkdir -p "$PRIV/bodies-v34"
# Free port 12362 if a stale server holds it. Kill by the LISTENER's pid (from ss),
# never `pkill -f serve_openai` — that pattern also matches this script's own
# command line and would SIGTERM the launch before it binds.
STALE=$(ss -ltnp 2>/dev/null | grep ':12362 ' | grep -oP 'pid=\K[0-9]+' | head -1)
if [ -n "$STALE" ]; then echo "freeing port 12362 (stale pid=$STALE)"; kill "$STALE" 2>/dev/null; sleep 3; fi
export SERVE_DEBUG_LOG="$PRIV/serve_debug_v34.jsonl"
export SERVE_DUMP_DIR="$PRIV/bodies-v34"
export SERVE_DUMP_BODIES=60
cd "$REPO"
echo "Serving artex-agentic-v34 (debug) on 0.0.0.0:12362  dumps -> $SERVE_DUMP_DIR"
exec "$PY" learning/serve_openai_debug.py \
  --adapter learning/artifacts/adapter-agentic-v34 \
  --model-name artex-agentic-v34 \
  --host 0.0.0.0 --port 12362 \
  --max-input-tokens 32768 --max-output-tokens 1536
