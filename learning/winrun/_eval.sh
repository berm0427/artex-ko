#!/usr/bin/env bash
# Shared: serve one adapter, wait for health, score the v33 holdout, tear down.
# Args: ADAPTER_DIR  MODEL_NAME  LABEL  OUTPUT_JSONL  PORT
# Single GPU: serves exactly one adapter at a time, then kills it on exit.
set -u
REPO=/mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko
PY=/home/c0d1n9/.venvs/artex-qlora-check/bin/python
ADAPTER=$1; MODEL=$2; LABEL=$3; OUT=$4; PORT=$5
cd "$REPO"
RT=.runtime/agentic-v33
mkdir -p "$RT"
"$PY" learning/serve_openai.py --adapter "$ADAPTER" --model-name "$MODEL" \
  --host 127.0.0.1 --port "$PORT" --max-input-tokens 4096 --max-output-tokens 1536 \
  > "$RT/serve_${MODEL}.log" 2>&1 &
SPID=$!
cleanup() { kill "$SPID" 2>/dev/null; sleep 2; kill -9 "$SPID" 2>/dev/null; }
trap cleanup EXIT
echo "waiting for http://127.0.0.1:$PORT/health ..."
ready=0
for _ in $(seq 1 150); do
  if "$PY" -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:$PORT/health',timeout=2)" >/dev/null 2>&1; then
    ready=1; break
  fi
  sleep 2
done
if [ "$ready" -ne 1 ]; then
  echo "SERVE_FAILED — see $RT/serve_${MODEL}.log"; exit 1
fi
"$PY" learning/eval_agentic_v33.py --holdout learning/agentic_holdout_v33.jsonl \
  --url "http://127.0.0.1:$PORT/v1" --model "$MODEL" --label "$LABEL" \
  --max-tokens 1536 --output "$OUT"
echo "EVAL_DONE -> $OUT"
