#!/usr/bin/env bash
# Serve v34 for ARTEX wiring (the opt-in "ARTEX v34 C" profile on port 12362).
# Differs from serve-v34.sh (local eval): binds 0.0.0.0 so the Windows
# netsh portproxy (127.0.0.1:12362 -> WSL_IP:12362) can reach it, and uses the
# model name the ARTEX profile expects. Stays running until Ctrl+C / window close.
#   ARTEX profile base_url : http://127.0.0.1:12362/v1  (via portproxy)
#   health (WSL side)      : http://127.0.0.1:12362/health -> model artex-agentic-v34
set -euo pipefail
REPO=/mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko
PY=/home/c0d1n9/.venvs/artex-qlora-check/bin/python
cd "$REPO"
echo "Serving artex-agentic-v34 on 0.0.0.0:12362 for ARTEX profile 'ARTEX v34 C' (Ctrl+C to stop)"
echo "Reminder: the Windows portproxy 127.0.0.1:12362 -> $(hostname -I | awk '{print $1}'):12362 must exist (admin netsh)."
exec "$PY" learning/serve_openai.py \
  --adapter learning/artifacts/adapter-agentic-v34 \
  --model-name artex-agentic-v34 --host 0.0.0.0 --port 12362 \
  --max-input-tokens 8192 --max-output-tokens 1536
