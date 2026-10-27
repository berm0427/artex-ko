#!/usr/bin/env bash
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
exec bash "$DIR/_eval.sh" \
  learning/artifacts/adapter-agentic-v34 \
  agentic-v34 candidate-v34 \
  learning/eval_v33_candidate_v34.jsonl 12354
