#!/usr/bin/env bash
# Baseline comparison: the pre-agentic adapter on the same holdout + same caps.
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
exec bash "$DIR/_eval.sh" \
  learning/artifacts/adapter-12-four-task-v5 \
  baseline-v5 baseline-v5 \
  learning/eval_v33_baseline_v5.jsonl 12360
