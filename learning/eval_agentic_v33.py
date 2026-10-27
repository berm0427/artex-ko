#!/usr/bin/env python3
"""v33 agentic holdout 채점기 — compaction·action 두 실패 모드를 기계적으로 측정.

`prepare_agentic_holdout_v33.py`가 만든 입력 전용 보류셋을 받아, OpenAI 호환
서버(serve_openai.py)에 각 케이스를 보내고 응답을 **규칙으로만** 채점한다.
자동으로 "풀이 성공"을 선언하지 않는다. 두 가지 결함만 본다.

compaction (실패 모드 A: 요약 대신 지시문 반복)
  - has_blocks   : <analysis>와 <summary> 블록이 모두 있는가.
  - sections>=8  : 9개 섹션 머리말 중 8개 이상이 등장하는가.
  - not_echoed   : /compact 지시문 문장을 그대로 복제하지 않았는가.
  - not_degen    : 같은 조각을 병적으로 반복하지 않았는가.
  - not_truncated: finish_reason이 length(출력 상한 절삭)가 아닌가.
  pass = 위 다섯을 모두 만족.

action (실패 모드 B: 조회만 반복, add_intent 미호출)
  - called       : 도구를 하나라도 호출했는가.
  - is_add_intent: 호출한 도구가 add_intent인가(list_*/graph_overview 아님).
  pass = is_add_intent. (서버가 반복/불완전 호출을 422로 막으면 called=False로 기록.)

사용법
    python eval_agentic_v33.py --holdout agentic_holdout_v33.jsonl \
        --url http://127.0.0.1:12346/v1 --model artex-qwen3-4b-four-task-v5 \
        --label baseline-v5 --output eval_v33_baseline_v5.jsonl
"""

from __future__ import annotations

import argparse
import json
import re
import urllib.error
import urllib.request
from pathlib import Path

SECTION_MARKERS = [
    "Primary Request",
    "Key Technical Concepts",
    "Files and Code",
    "Errors and",
    "Problem Solving",
    "user messages",
    "Pending Tasks",
    "Current Work",
    "Optional Next Step",
]

# 지시문을 그대로 베꼈는지 판별할 고유 조각(지시문에만 있고 정상 요약엔 없을 문장).
INSTRUCTION_FRAGMENTS = [
    "Respond with TEXT ONLY",
    "Do NOT call any tools",
    "capturing technical details needed to continue",
    "must have these sections",
    "Create a detailed summary of the conversation so far",
]

LISTING_NAMES = {"list_intents", "list_facts", "list_notes", "list_findings",
                 "list_files", "graph_overview"}


def post_chat(url: str, model: str, messages: list[dict], tools: list | None,
              max_tokens: int, timeout: float) -> tuple[int, dict]:
    body: dict = {"model": model, "messages": messages, "temperature": 0,
                  "max_tokens": max_tokens}
    if tools:
        body["tools"] = tools
        body["tool_choice"] = "auto"
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url.rstrip("/") + "/chat/completions", data=data,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        detail = e.read().decode("utf-8", "replace")
        try:
            detail = json.loads(detail)
        except Exception:
            pass
        return e.code, {"error": detail}


def has_degenerate_repeat(text: str, n: int = 8, threshold: int = 4) -> bool:
    """같은 n-단어 조각이 threshold번 넘게 반복되면 병적 반복으로 본다."""
    words = text.split()
    if len(words) < n * threshold:
        return False
    seen: dict[str, int] = {}
    for i in range(len(words) - n + 1):
        gram = " ".join(words[i:i + n])
        seen[gram] = seen.get(gram, 0) + 1
        if seen[gram] >= threshold:
            return True
    return False


def grade_compaction(status: int, resp: dict) -> dict:
    if status != 200:
        return {"pass": False, "error": resp.get("error"), "status": status}
    choice = (resp.get("choices") or [{}])[0]
    text = (choice.get("message") or {}).get("content") or ""
    finish = choice.get("finish_reason")
    sections = sum(1 for m in SECTION_MARKERS if m.lower() in text.lower())
    echoed = any(frag.lower() in text.lower() for frag in INSTRUCTION_FRAGMENTS)
    degen = has_degenerate_repeat(text)
    has_blocks = "<analysis>" in text and "<summary>" in text
    truncated = finish == "length"
    metrics = {
        "has_blocks": has_blocks,
        "sections": sections,
        "sections_ok": sections >= 8,
        "not_echoed": not echoed,
        "not_degen": not degen,
        "not_truncated": not truncated,
        "finish_reason": finish,
        "chars": len(text),
    }
    metrics["pass"] = bool(has_blocks and sections >= 8 and not echoed
                           and not degen and not truncated)
    return metrics


def extract_tool_name(choice: dict) -> str | None:
    msg = choice.get("message") or {}
    calls = msg.get("tool_calls")
    if calls:
        fn = (calls[0] or {}).get("function") or {}
        return fn.get("name")
    # 텍스트에 <tool_call>{"name": ...}</tool_call>로 온 경우도 처리.
    content = msg.get("content") or ""
    m = re.search(r"<tool_call>\s*(\{.*?\})\s*</tool_call>", content, re.S)
    if m:
        try:
            return json.loads(m.group(1)).get("name")
        except Exception:
            return None
    return None


def grade_action(status: int, resp: dict) -> dict:
    if status != 200:
        # serve가 반복/불완전 호출을 422로 막은 경우 → 행동 미실행으로 기록.
        return {"pass": False, "called": False, "error": resp.get("error"),
                "status": status}
    choice = (resp.get("choices") or [{}])[0]
    name = extract_tool_name(choice)
    called = name is not None
    is_add = name == "add_intent"
    is_listing = name in LISTING_NAMES
    return {"pass": bool(is_add), "called": called, "tool": name,
            "is_add_intent": is_add, "is_listing": is_listing,
            "finish_reason": choice.get("finish_reason")}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--holdout", type=Path, required=True)
    ap.add_argument("--url", required=True, help="OpenAI 호환 base URL, 예: http://127.0.0.1:12346/v1")
    ap.add_argument("--model", required=True)
    ap.add_argument("--label", default="")
    ap.add_argument("--output", type=Path, required=True)
    ap.add_argument("--max-tokens", type=int, default=768,
                    help="요청 출력 상한. 절삭 교란을 보려면 서버 상한 이상으로.")
    ap.add_argument("--timeout", type=float, default=300.0)
    args = ap.parse_args()

    rows = [json.loads(l) for l in args.holdout.read_text(encoding="utf-8").splitlines() if l.strip()]
    results = []
    for r in rows:
        status, resp = post_chat(args.url, args.model, r["messages"], r.get("tools"),
                                 args.max_tokens, args.timeout)
        if r["kind"] == "compaction":
            graded = grade_compaction(status, resp)
        else:
            graded = grade_action(status, resp)
        results.append({"case_id": r["case_id"], "kind": r["kind"],
                        "label": args.label, "model": args.model, "metrics": graded})

    def rate(kind: str) -> tuple[int, int]:
        sub = [x for x in results if x["kind"] == kind]
        return sum(x["metrics"].get("pass") for x in sub), len(sub)

    cp, ct = rate("compaction")
    ap_, at = rate("action")
    summary = {"label": args.label, "model": args.model,
               "compaction_pass": f"{cp}/{ct}", "action_pass": f"{ap_}/{at}"}

    with args.output.open("w", encoding="utf-8") as f:
        for x in results:
            f.write(json.dumps(x, ensure_ascii=False) + "\n")
        f.write(json.dumps({"summary": summary}, ensure_ascii=False) + "\n")

    print(json.dumps(summary, ensure_ascii=False))
    for x in results:
        m = x["metrics"]
        flag = "PASS" if m.get("pass") else "fail"
        extra = (f"sec={m.get('sections')} echo={not m.get('not_echoed', True)} "
                 f"trunc={not m.get('not_truncated', True)}" if x["kind"] == "compaction"
                 else f"tool={m.get('tool')}")
        print(f"  [{flag}] {x['kind']:10s} {x['case_id']:35s} {extra}")


if __name__ == "__main__":
    main()
