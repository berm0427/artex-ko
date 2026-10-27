"""QLoRA prototype on reviewed ARTEX records or the legacy 12 cards.

The legacy 12 cases were ALL used for the earlier pipeline check; they cannot
measure generalization of that adapter. This is not a solved-challenge claim.
"""

from __future__ import annotations

import argparse
import json
import random
from pathlib import Path


SYSTEM = (
    "너는 허가된 격리 환경의 보안 실험 계획자다. 확인된 사실과 추론을 "
    "구분하고, 입력 도달성·목표 출력 경로·원시 응답을 기준으로 다음 검증을 "
    "제안한다. 실제 대상 응답 없이 성공을 선언하지 않는다."
)


def load_rows(path: Path, manifest_path: Path) -> list[dict[str, str]]:
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    case_ids = {item["id"] for item in manifest["cases"]}
    row_ids = [row["case_id"] for row in rows]
    if len(rows) != 12 or len(set(row_ids)) != 12 or set(row_ids) != case_ids:
        raise ValueError("method cards must cover each frozen manifest case exactly once")
    for row in rows:
        if not row.get("prompt") or not row.get("completion"):
            raise ValueError(f"empty prompt/completion: {row['case_id']}")
    return rows


def load_reviewed_rows(path: Path, manifest_path: Path) -> list[dict[str, str]]:
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    splits = {item["id"]: item["split"] for item in manifest["cases"]}
    if not rows:
        raise ValueError("reviewed dataset is empty")
    for row in rows:
        if splits.get(row.get("case_id")) != "train":
            raise ValueError(f"non-train or unknown case: {row.get('case_id')}")
        if not all(row.get(field) for field in ("record_id", "raw_sha256", "prompt", "completion")):
            raise ValueError("reviewed row lacks provenance or text")
    if len({row["record_id"] for row in rows}) != len(rows):
        raise ValueError("duplicate reviewed record")
    return rows


def load_general_rows(path: Path, manifest_path: Path) -> list[dict[str, str]]:
    """Load deterministic harmless examples without tool-observation claims."""
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    allowed = {item["id"] for item in manifest["cases"] if item["split"] == "train"}
    if not rows or {row.get("case_id") for row in rows} != allowed:
        raise ValueError("general dataset must cover the manifest train split exactly")
    for row in rows:
        if row.get("source") != "generated_general_arithmetic_v2":
            raise ValueError("unexpected general dataset source")
        if not row.get("prompt") or not row.get("completion") or not row.get("oracle"):
            raise ValueError("incomplete general dataset row")
        if "tool" in row or "messages" in row:
            raise ValueError("general dataset must not contain tool observations")
        answer = json.loads(row["completion"])
        if not isinstance(answer.get("result"), int):
            raise ValueError("general completion must contain an integer result")
    return rows


def load_stability_rows(path: Path, manifest_path: Path) -> list[dict[str, str]]:
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if len(rows) != manifest["counts"]["train"]:
        raise ValueError("stability train count mismatch")
    for row in rows:
        if row.get("source") != "synthetic_stability_guidance_v1" or row.get("split") != "train":
            raise ValueError("unexpected stability dataset row")
        completion = json.loads(row["completion"])
        if set(completion) != {"logic_defect", "debug_signals", "stability_fix"}:
            raise ValueError("stability completion sections mismatch")
        if completion["debug_signals"].get("observed_logs") != []:
            raise ValueError("stability row claims unobserved logs")
    return rows


def load_defensive_rows(path: Path, manifest_path: Path) -> list[dict[str, str]]:
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if len(rows) != manifest["counts"]["train"]:
        raise ValueError("defensive train count mismatch")
    for row in rows:
        if row.get("source") != "synthetic_defensive_reasoning_v1" or row.get("split") != "train":
            raise ValueError("unexpected defensive dataset row")
        if "tools" in row or "messages" in row:
            raise ValueError("defensive dataset must not contain tool observations")
        json.loads(row["completion"])
    return rows


def load_transform_trace_rows(path: Path, manifest_path: Path) -> list[dict[str, str]]:
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if len(rows) != manifest["counts"]["train"]:
        raise ValueError("transform/trace train count mismatch")
    if {row.get("source") for row in rows} != {"synthetic_transform_trace_v2"}:
        raise ValueError("unexpected transform/trace source")
    if any(row.get("split") != "train" for row in rows):
        raise ValueError("non-train transform/trace row")
    if len({row["sample_id"] for row in rows}) != len(rows):
        raise ValueError("duplicate transform/trace sample")
    for row in rows:
        json.loads(row["completion"])
        if any(term in row["prompt"] for term in ("Google CTF", "JS Safe", "Gatekeeper")):
            raise ValueError("forbidden final-evaluation reference in training")
    return rows


def load_ctf_action_rows(path: Path, manifest_path: Path) -> list[dict]:
    """Load manually reviewed tool-call examples from a frozen source split."""
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    cases = {item["case_id"]: item for item in manifest["cases"]}
    if not rows:
        raise ValueError("CTF action dataset is empty")
    identifiers = [row.get("sample_id") or row.get("case_id") for row in rows]
    if len(set(identifiers)) != len(rows):
        raise ValueError("duplicate CTF action sample")
    for row in rows:
        case = cases.get(row.get("case_id"))
        if case is None or case["split"] != "train":
            raise ValueError(f"non-train CTF case: {row.get('case_id')}")
        if row.get("source") != manifest["source"] or row.get("license") != manifest["license"] or row.get("source_revision") != manifest["revision"]:
            raise ValueError("CTF provenance mismatch")
        if not any(file["path"] == row.get("source_path") and file["git_blob"] == row.get("source_blob") for file in case["files"]):
            raise ValueError(f"CTF source blob mismatch: {row['case_id']}")
        if not row.get("review") or not row.get("prompt") or not row.get("completion") or not row.get("tools"):
            raise ValueError(f"incomplete reviewed CTF action: {row['case_id']}")
        if row.get("messages"):
            if not isinstance(row["messages"], list) or not all(isinstance(message, dict) and message.get("role") in {"user", "assistant", "tool"} for message in row["messages"]):
                raise ValueError(f"invalid CTF conversation: {row['case_id']}")
        if not row["completion"].startswith("<tool_call>"):
            raise ValueError(f"tool call target missing: {row['case_id']}")
    return rows


def load_agentic_rows(path: Path, manifest_path: Path) -> list[dict]:
    """Load the synthetic agentic SFT set that fixes the v32 failure modes.

    Rows already carry their own system message (compaction: summarizer;
    action: ARTEX main agent) plus the user turn, so train these with
    --no-training-system to avoid a duplicated system prompt. Compaction
    targets are an <analysis>+<summary> block; action targets are a single
    <tool_call>. The training loop already consumes messages/tools/completion.
    """
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if not rows:
        raise ValueError("agentic dataset is empty")
    if len(rows) != manifest["counts"]["train"]:
        raise ValueError("agentic train count mismatch")
    if len({row.get("sample_id") or row.get("case_id") for row in rows}) != len(rows):
        raise ValueError("duplicate agentic sample")
    for row in rows:
        if row.get("source") != manifest["source"]:
            raise ValueError("agentic provenance mismatch")
        if row.get("kind") not in {"compaction", "action"}:
            raise ValueError(f"unknown agentic kind: {row.get('case_id')}")
        if not isinstance(row.get("messages"), list) or not row["messages"]:
            raise ValueError(f"agentic row lacks messages: {row.get('case_id')}")
        if not all(isinstance(m, dict) and m.get("role") in {"system", "user", "assistant", "tool"}
                   for m in row["messages"]):
            raise ValueError(f"invalid agentic conversation: {row.get('case_id')}")
        if not row.get("completion"):
            raise ValueError(f"agentic row lacks completion: {row.get('case_id')}")
        if row["kind"] == "compaction":
            if "<analysis>" not in row["completion"] or "<summary>" not in row["completion"]:
                raise ValueError(f"compaction target missing blocks: {row.get('case_id')}")
            if row.get("tools"):
                raise ValueError(f"compaction row must not offer tools: {row.get('case_id')}")
        else:
            if not row.get("tools"):
                raise ValueError(f"action row lacks tools: {row.get('case_id')}")
            if not row["completion"].lstrip().startswith("<tool_call>"):
                raise ValueError(f"action target not a tool call: {row.get('case_id')}")
    return rows


def train(model_path: Path, output: Path, rows: list[dict[str, str]], *, note: str,
          epochs: int = 1, learning_rate: float = 5e-5,
          training_system: bool = True, lora_rank: int = 4,
          all_linear: bool = False, checkpoint_every: int = 0,
          shuffle_seed: int | None = None,
          resume_adapter: Path | None = None,
          max_seq_length: int = 768) -> None:
    import torch
    from unsloth import FastLanguageModel

    if output.exists():
        raise FileExistsError(f"refusing to overwrite adapter: {output}")
    if not torch.cuda.is_available():
        raise RuntimeError("CUDA GPU is required for this QLoRA check")
    if not 1 <= epochs <= 20 or not 0 < learning_rate <= 2e-4:
        raise ValueError("epochs must be 1..20 and learning rate must be 0..2e-4")
    if checkpoint_every < 0 or (checkpoint_every and epochs % checkpoint_every):
        raise ValueError("checkpoint_every must divide epochs")
    if lora_rank not in {4, 8, 16}:
        raise ValueError("LoRA rank must be 4, 8, or 16")
    if not 768 <= max_seq_length <= 4096:
        raise ValueError("max_seq_length must be 768..4096")
    model, tokenizer = FastLanguageModel.from_pretrained(
        model_name=str(resume_adapter or model_path), max_seq_length=max_seq_length,
        load_in_4bit=True, dtype=torch.bfloat16,
    )
    if resume_adapter:
        if not any(p.requires_grad for p in model.parameters()):
            raise RuntimeError("resumed adapter has no trainable parameters")
    else:
        targets = (["q_proj", "k_proj", "v_proj", "o_proj", "gate_proj", "up_proj", "down_proj"]
                   if all_linear else ["q_proj", "v_proj"])
        model = FastLanguageModel.get_peft_model(
            model, r=lora_rank, target_modules=targets, lora_alpha=2 * lora_rank,
            lora_dropout=0, bias="none", use_gradient_checkpointing="unsloth",
        )
    model.train()
    optimizer = torch.optim.AdamW((p for p in model.parameters() if p.requires_grad), lr=learning_rate)
    torch.cuda.reset_peak_memory_stats()
    losses = []
    training_rows = []
    rng = random.Random(shuffle_seed) if shuffle_seed is not None else None
    for _ in range(epochs):
        epoch_rows = list(rows)
        if rng is not None:
            rng.shuffle(epoch_rows)
        training_rows.extend(epoch_rows)
    checkpoint_paths = [output.with_name(f"{output.name}-e{epoch}")
                        for epoch in range(checkpoint_every, epochs, checkpoint_every)] if checkpoint_every else []
    if any(path.exists() for path in checkpoint_paths):
        raise FileExistsError("refusing to overwrite training checkpoint")
    for step, row in enumerate(training_rows, 1):
        messages = list(row.get("messages") or [{"role": "user", "content": row["prompt"]}])
        if training_system:
            messages.insert(0, {"role": "system", "content": SYSTEM})
        prompt = tokenizer.apply_chat_template(
            messages,
            tools=row.get("tools") or None,
            tokenize=False, add_generation_prompt=True, enable_thinking=False,
        )
        answer = row["completion"] + tokenizer.eos_token
        prompt_ids = tokenizer(prompt, add_special_tokens=False)["input_ids"]
        full_ids = tokenizer(prompt + answer, add_special_tokens=False)["input_ids"]
        if len(full_ids) > max_seq_length:
            raise ValueError(f"example exceeds context window: {row['case_id']}")
        labels = [-100] * len(prompt_ids) + full_ids[len(prompt_ids):]
        if all(value == -100 for value in labels):
            raise ValueError(f"empty assistant target: {row['case_id']}")
        input_ids = torch.tensor([full_ids], device="cuda")
        label_ids = torch.tensor([labels], device="cuda")
        optimizer.zero_grad(set_to_none=True)
        loss = model(input_ids=input_ids, labels=label_ids).loss
        if not torch.isfinite(loss):
            raise RuntimeError(f"non-finite loss: {row['case_id']}")
        loss.backward()
        optimizer.step()
        losses.append(round(float(loss.detach()), 4))
        print(json.dumps({"step": step, "epoch": (step - 1) // len(rows) + 1,
                          "case_id": row["case_id"], "sample_id": row.get("sample_id", row["case_id"]),
                          "loss": losses[-1]}, ensure_ascii=False), flush=True)
        if checkpoint_every and step % (len(rows) * checkpoint_every) == 0 and step < len(training_rows):
            checkpoint_epoch = step // len(rows)
            checkpoint = output.with_name(f"{output.name}-e{checkpoint_epoch}")
            checkpoint.parent.mkdir(parents=True, exist_ok=True)
            model.save_pretrained(str(checkpoint))
            tokenizer.save_pretrained(str(checkpoint))
            print(json.dumps({"checkpoint": str(checkpoint), "epoch": checkpoint_epoch,
                              "loss": losses[-1]}, ensure_ascii=False), flush=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    model.save_pretrained(str(output))
    tokenizer.save_pretrained(str(output))
    print(json.dumps({"adapter": str(output), "steps": len(losses),
                      "loss_first": losses[0], "loss_last": losses[-1],
                      "peak_gib": round(torch.cuda.max_memory_allocated() / 2**30, 2),
                      "epochs": epochs, "learning_rate": learning_rate,
                      "training_system": training_system, "lora_rank": lora_rank,
                      "all_linear": all_linear, "checkpoint_every": checkpoint_every,
                      "shuffle_seed": shuffle_seed, "note": note},
                     ensure_ascii=False), flush=True)


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", type=Path, required=True)
    parser.add_argument("--resume-adapter", type=Path,
                        help="continue a prior LoRA adapter into a new output directory")
    parser.add_argument("--max-seq-length", type=int, default=768)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--reviewed-dataset", type=Path,
                        help="immutable JSONL exported from approved ARTEX train records")
    parser.add_argument("--ctf-actions-dataset", type=Path,
                        help="manually reviewed, source-grounded tool action examples")
    parser.add_argument("--ctf-manifest", type=Path,
                        help="frozen licensed CTF source and challenge-level split")
    parser.add_argument("--general-dataset", type=Path,
                        help="deterministic harmless general arithmetic JSONL")
    parser.add_argument("--general-manifest", type=Path,
                        help="manifest for the harmless general dataset")
    parser.add_argument("--stability-dataset", type=Path,
                        help="synthetic three-section stability guidance JSONL")
    parser.add_argument("--stability-manifest", type=Path,
                        help="manifest for the stability guidance dataset")
    parser.add_argument("--defensive-dataset", type=Path,
                        help="synthetic defensive reasoning JSONL")
    parser.add_argument("--defensive-manifest", type=Path,
                        help="manifest for the defensive reasoning dataset")
    parser.add_argument("--transform-trace-dataset", type=Path,
                        help="synthetic harmless string/trace/patch JSONL")
    parser.add_argument("--transform-trace-manifest", type=Path,
                        help="manifest for synthetic string/trace/patch data")
    parser.add_argument("--agentic-dataset", type=Path,
                        help="synthetic agentic SFT (compaction + add_intent action) JSONL")
    parser.add_argument("--agentic-manifest", type=Path,
                        help="manifest for the agentic SFT dataset")
    parser.add_argument("--include-all-12", action="store_true",
                        help="acknowledge that historical validation/test cases become training cases")
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--epochs", type=int, default=1)
    parser.add_argument("--learning-rate", type=float, default=5e-5)
    parser.add_argument("--no-training-system", action="store_true",
                        help="align with user-only Chat Completions requests")
    parser.add_argument("--reviewed-natural-only", action="store_true",
                        help="exclude structured reviewed examples when testing live-question alignment")
    parser.add_argument("--lora-rank", type=int, default=4)
    parser.add_argument("--lora-all-linear", action="store_true")
    parser.add_argument("--checkpoint-every", type=int, default=0)
    parser.add_argument("--shuffle-seed", type=int)
    args = parser.parse_args()
    if not args.reviewed_dataset and not args.include_all_12 and not args.ctf_actions_dataset and not args.general_dataset and not args.stability_dataset and not args.defensive_dataset and not args.transform_trace_dataset and not args.agentic_dataset:
        parser.error("select a reviewed dataset, CTF action dataset, agentic dataset, and/or --include-all-12")
    rows = []
    if args.include_all_12:
        rows.extend(load_rows(root / "learning" / "seed_methods_12.jsonl",
                              root / "experiments" / "benchmark12" / "manifest.json"))
    if args.reviewed_dataset:
        if not args.manifest:
            parser.error("--manifest is required with --reviewed-dataset")
        reviewed = load_reviewed_rows(args.reviewed_dataset, args.manifest)
        if args.reviewed_natural_only:
            reviewed = [row for row in reviewed if row.get("format") == "natural"]
            if not reviewed:
                parser.error("no natural reviewed records remain")
        rows.extend(reviewed)
    elif args.reviewed_natural_only:
        parser.error("--reviewed-natural-only requires --reviewed-dataset")
    if args.ctf_actions_dataset:
        if not args.ctf_manifest:
            parser.error("--ctf-manifest is required with --ctf-actions-dataset")
        rows.extend(load_ctf_action_rows(args.ctf_actions_dataset, args.ctf_manifest))
    if args.general_dataset:
        if not args.general_manifest:
            parser.error("--general-manifest is required with --general-dataset")
        rows.extend(load_general_rows(args.general_dataset, args.general_manifest))
    if args.stability_dataset:
        if not args.stability_manifest:
            parser.error("--stability-manifest is required with --stability-dataset")
        rows.extend(load_stability_rows(args.stability_dataset, args.stability_manifest))
    if args.defensive_dataset:
        if not args.defensive_manifest:
            parser.error("--defensive-manifest is required with --defensive-dataset")
        rows.extend(load_defensive_rows(args.defensive_dataset, args.defensive_manifest))
    if args.transform_trace_dataset:
        if not args.transform_trace_manifest:
            parser.error("--transform-trace-manifest is required with --transform-trace-dataset")
        rows.extend(load_transform_trace_rows(args.transform_trace_dataset,
                                              args.transform_trace_manifest))
    if args.agentic_dataset:
        if not args.agentic_manifest:
            parser.error("--agentic-manifest is required with --agentic-dataset")
        rows.extend(load_agentic_rows(args.agentic_dataset, args.agentic_manifest))
    if len({row.get("sample_id") or row["case_id"] for row in rows}) != len(rows) and not args.reviewed_dataset:
        raise ValueError("duplicate training sample ID")
    train(args.model, args.output, rows,
          epochs=args.epochs, learning_rate=args.learning_rate,
          training_system=not args.no_training_system,
          lora_rank=args.lora_rank, all_linear=args.lora_all_linear,
          checkpoint_every=args.checkpoint_every, shuffle_seed=args.shuffle_seed,
          resume_adapter=args.resume_adapter, max_seq_length=args.max_seq_length, note=(
        "synthetic agentic SFT (compaction summarization + add_intent action); evaluate on unseen holdout only"
        if args.agentic_dataset and not args.reviewed_dataset and not args.ctf_actions_dataset and not args.include_all_12 else
        "licensed CTF source-grounded tool actions only; train challenges are excluded from evaluation"
        if args.ctf_actions_dataset and not args.reviewed_dataset and not args.include_all_12 else
        "reviewed train records, historical 12 and licensed CTF actions; all included cases are train-contaminated"
        if args.ctf_actions_dataset and args.reviewed_dataset and args.include_all_12 else
        "reviewed train records plus historical 12; all included cases are contaminated for evaluation"
        if args.reviewed_dataset and args.include_all_12 else
        "reviewed train-only records; capability requires independent holdout"
        if args.reviewed_dataset else
        "deterministic harmless general arithmetic; independent holdout required"
        if args.general_dataset else
        "synthetic three-section stability guidance; independent final set required"
        if args.stability_dataset else
        "synthetic transform, bounded trace and safe patch reasoning; independent final set required"
        if args.transform_trace_dataset else
        "synthetic defensive reasoning; independent final set required"
        if args.defensive_dataset else
        "pipeline check only; all 12 cases are contaminated for evaluation"))


if __name__ == "__main__":
    main()
