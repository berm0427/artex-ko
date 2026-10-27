#!/usr/bin/env bash
# Serve the v34 adapter on an OpenAI-compatible endpoint (foreground).
# Stays running until Ctrl+C or the window is closed.
#   health : http://127.0.0.1:12354/health
#   api    : http://127.0.0.1:12354/v1
set -euo pipefail
REPO=/mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko
PY=/home/c0d1n9/.venvs/artex-qlora-check/bin/python
cd "$REPO"
echo "Serving adapter-agentic-v34 on http://127.0.0.1:12354  (Ctrl+C to stop)"
exec "$PY" learning/serve_openai.py \
  --adapter learning/artifacts/adapter-agentic-v34 \
  --model-name agentic-v34 --host 127.0.0.1 --port 12354 \
  --max-input-tokens 4096 --max-output-tokens 1536
