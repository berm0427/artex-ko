#!/usr/bin/env bash
# Train the v34 agentic adapter (QLoRA, rank8, all-linear, 5 epochs).
# --no-training-system is MANDATORY: the agentic rows carry their own system
# prompt, so adding another would duplicate it and corrupt training.
set -euo pipefail
REPO=/mnt/c/Users/berm0/Documents/Codex/2026-09-15/new-chat/work/artex-ko
PY=/home/c0d1n9/.venvs/artex-qlora-check/bin/python
BASE=/mnt/c/Users/berm0/Documents/Codex/models/Qwen3-4B-unsloth-bnb-4bit
cd "$REPO"
"$PY" learning/finetune_4b.py \
  --model "$BASE" \
  --agentic-dataset learning/agentic_sft_v2/train.jsonl \
  --agentic-manifest learning/agentic_sft_v2/manifest.json \
  --no-training-system \
  --epochs 5 --lora-rank 8 --lora-all-linear \
  --max-seq-length 768 \
  --output learning/artifacts/adapter-agentic-v34
echo "TRAIN_DONE -> learning/artifacts/adapter-agentic-v34"
