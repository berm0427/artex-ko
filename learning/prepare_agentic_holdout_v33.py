#!/usr/bin/env python3
"""v33 agentic holdout 생성기 — compaction·action 두 실패 모드의 일반화 평가용.

배경
----
`finding_v32_introspection_loop_20261026.md`가 확정한 v32 무한 루프의 두 원인:
  (A) compaction(대화 요약) 턴에서 9섹션 요약 대신 지시문을 반복.
  (B) 열린 의도가 없는데 add_intent를 안 부르고 list_* 조회만 반복.
`agentic_sft_v1/train.jsonl`이 이 둘을 교정하는 학습셋이지만 평가셋이 없다.
finding도 "평가는 학습 미포함 문제로"라고 못박았다.

이 파일은 그 **보류셋(입력 전용)**을 만든다. 정답(모범 요약/행동)을 넣지 않는다.
대신 `eval_agentic_v33.py`가 모델을 돌려 두 실패 모드를 **기계적으로** 채점한다:
  - compaction: 출력에 9섹션이 모두 있고, /compact 지시문을 그대로 복제하지 않았는가.
  - action: add_intent를 호출했는가(list_* 조회만 반복하지 않았는가).

왜 Google CTF 2025인가
----------------------
`google_ctf_2024_2025_manifest.json`이 2024 quals=train, 2025 quals=holdout로
문제 단위 분리를 이미 고정했다(Apache-2.0, 고정 revision). 또한 train.jsonl의
시나리오는 redis/web/binary 합성이라 2025 문제와 겹치지 않는다. 따라서 2025
문제로 만든 입력은 어느 어댑터에도 학습되지 않은 것으로 간주할 수 있다.

안전 규율
---------
정적 소스 정찰 수준만 서술한다. flag 값·목표 문자열·동작하는 익스플로잇을
넣지 않는다. 각 transcript는 "파일을 읽어 입력 도달성·분기를 파악한다" 수준이다.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

# train.jsonl 1번 줄에서 그대로 가져온 ARTEX/Claude Code compaction 지시문.
# 모델이 이 텍스트를 복제하면 실패(A). 채점기가 이 문장들의 복제를 검사한다.
COMPACTION_SYSTEM = "You are a helpful AI assistant tasked with summarizing conversations."
COMPACTION_INSTRUCTION = (
    "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools. Your entire response "
    "must be an <analysis> block followed by a <summary> block.\n"
    "Create a detailed summary of the conversation so far, capturing technical details "
    "needed to continue without losing context. The <summary> must have these sections: "
    "1. Primary Request and Intent, 2. Key Technical Concepts, 3. Files and Code Sections, "
    "4. Errors and fixes, 5. Problem Solving, 6. All user messages, 7. Pending Tasks, "
    "8. Current Work, 9. Optional Next Step."
)

# train.jsonl action 샘플에서 그대로 가져온 시스템 프롬프트와 add_intent 스키마.
ACTION_SYSTEM = (
    "너는 ARTEX 자율 침투 테스트의 메인 에이전트다. 목표를 향해 frontier를 넓히는 "
    "구체적 행동을 선택하라. 열린 의도(open intent)가 없고 아직 할 일이 남았다면, "
    "조회만 반복하지 말고 add_intent로 다음 탐색 방향을 만들어라."
)
ADD_INTENT_TOOL = {
    "type": "function",
    "function": {
        "name": "add_intent",
        "description": (
            "새 탐색 방향을 frontier에 추가한다. summary에 무엇을 왜 탐색·검증할지 "
            "한 문장으로 적는다. 여러 방향은 intents 배열로 제출한다."
        ),
        "parameters": {
            "type": "object",
            "properties": {
                "summary": {"type": "string", "description": "단일 탐색 방향: 무엇을 왜 할지 한 문장"},
                "priority": {"type": "integer", "description": "우선순위 0~10(기본 5)"},
                "asset_ids": {"type": "array", "items": {"type": "integer"}},
                "parent_ids": {"type": "array", "items": {"type": "integer"}},
            },
        },
    },
}

# 조회 전용 도구들도 함께 제공해, 모델이 list_*로 "도망칠" 선택지를 남긴다.
# 그래야 add_intent를 고르는 것이 진짜 결정인지 측정된다(실패 모드 B).
LIST_TOOLS = [
    {"type": "function", "function": {"name": "list_intents",
     "description": "현재 frontier의 의도 목록을 반환한다.",
     "parameters": {"type": "object", "properties": {}}}},
    {"type": "function", "function": {"name": "list_facts",
     "description": "지금까지 수집된 사실 목록을 반환한다.",
     "parameters": {"type": "object", "properties": {}}}},
    {"type": "function", "function": {"name": "graph_overview",
     "description": "작업 그래프의 개요를 반환한다.",
     "parameters": {"type": "object", "properties": {}}}},
]

# 2025 holdout 문제(정적 소스 정찰 수준). 각 항목은 입력만 만든다.
# transcript는 짧고, 이미 몇 단계 진행된 뒤 "요약"/"다음 행동"을 요구하는 지점.
CASES = [
    {
        "case_id": "google-ctf-2025-web-sourceless",
        "goal": "제공된 로컬 웹앱의 요청 처리 경로를 정적 소스로 파악한다",
        "transcript": [
            ("USER", "격리된 로컬 환경에서 제공된 웹앱의 요청 처리 경로를 정적으로만 분석하라. 원격 요청 금지."),
            ("ASSISTANT", "목표를 '라우트와 입력 검증 지점을 소스로 식별'로 설정."),
            ("ASSISTANT(tool list_files)", "package.json, app.mjs 확인."),
            ("ASSISTANT(tool Read package.json)", "진입점이 app.mjs, express 의존성 존재."),
            ("ASSISTANT", "다음은 app.mjs의 라우트 정의와 미들웨어 순서를 읽을 계획."),
        ],
        "facts": "진입점은 app.mjs, express 사용. 아직 라우트 정의는 안 읽음.",
    },
    {
        "case_id": "google-ctf-2025-pwn-multiarch-2",
        "goal": "제공된 로컬 바이너리 로더의 인자 처리 분기를 정적 소스로 파악한다",
        "transcript": [
            ("USER", "제공된 로컬 바이너리의 입력 처리 분기를 정적 소스로만 분석하라."),
            ("ASSISTANT", "목표를 'main의 인자 개수에 따른 분기 식별'로 설정."),
            ("ASSISTANT(tool list_files)", "main.c, Makefile 확인."),
            ("ASSISTANT(tool Read main.c head)", "argc 분기와 load_program 호출 흔적 확인."),
            ("ASSISTANT", "다음은 load_program 호출 조건과 실패 코드 경로를 확인할 계획."),
        ],
        "facts": "main.c에 argc 분기와 load_program 호출 존재. 실패 코드 경로는 아직 미확인.",
    },
    {
        "case_id": "google-ctf-2025-rev-cgb",
        "goal": "제공된 로컬 ROM의 기본 실행과 초기 출력을 확인한다",
        "transcript": [
            ("USER", "제공된 로컬 ROM을 격리 환경에서 한 번 실행해 초기 동작만 관찰하라."),
            ("ASSISTANT", "목표를 '에뮬레이터로 ROM 로딩·초기 화면 관찰'로 설정."),
            ("ASSISTANT(tool list_files)", "rom.gbc, readme 확인."),
            ("ASSISTANT(tool Read readme)", "지정 에뮬레이터와 실행 명령 형식 확인."),
            ("ASSISTANT", "다음은 에뮬레이터로 ROM을 한 번 실행해 초기 화면을 기록할 계획."),
        ],
        "facts": "실행 명령 형식 확인됨. 아직 실제 실행·관찰은 안 함.",
    },
]


def transcript_text(turns: list[tuple[str, str]]) -> str:
    body = "\n".join(f"{role}: {text}" for role, text in turns)
    return f"아래는 지금까지의 대화입니다.\n\n{body}"


def compaction_record(case: dict) -> dict:
    user = f"{transcript_text(case['transcript'])}\n\n{COMPACTION_INSTRUCTION}"
    return {
        "case_id": case["case_id"],
        "kind": "compaction",
        "split": "holdout",
        "source": "google_ctf_2025_holdout_v33",
        "source_revision": "f683ff991cac26f7673d2f2de5ac9d226b95b847",
        "messages": [
            {"role": "system", "content": COMPACTION_SYSTEM},
            {"role": "user", "content": user},
        ],
    }


def action_record(case: dict) -> dict:
    user = (
        f"현재 상태: 목표=[{case['goal']}]. open_intents=[]. "
        f"facts=[{case['facts']}]. 열린 의도가 없다. 다음 탐색 방향을 add_intent로 만들어라."
    )
    return {
        "case_id": case["case_id"],
        "kind": "action",
        "split": "holdout",
        "source": "google_ctf_2025_holdout_v33",
        "source_revision": "f683ff991cac26f7673d2f2de5ac9d226b95b847",
        "messages": [
            {"role": "system", "content": ACTION_SYSTEM},
            {"role": "user", "content": user},
        ],
        # add_intent 외에 list_* 선택지도 함께 줘서 "결정"을 진짜로 측정한다.
        "tools": [ADD_INTENT_TOOL, *LIST_TOOLS],
    }


def build() -> list[dict]:
    rows: list[dict] = []
    for case in CASES:
        rows.append(compaction_record(case))
        rows.append(action_record(case))
    return rows


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--out", type=Path, default=Path("agentic_holdout_v33.jsonl"))
    ap.add_argument("--manifest", type=Path, default=Path("agentic_holdout_v33_manifest.json"))
    args = ap.parse_args()

    rows = build()
    text = "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in rows)
    args.out.write_text(text, encoding="utf-8")

    manifest = {
        "version": 33,
        "purpose": "unseen holdout for v32 compaction+action failure modes",
        "split_rule": "Google CTF 2025 quals (holdout per google_ctf_2024_2025_manifest.json)",
        "source_revision": "f683ff991cac26f7673d2f2de5ac9d226b95b847",
        "input_only": True,
        "grading": "mechanical, see eval_agentic_v33.py",
        "safety": ["no flag values", "no target strings", "static-source recon only"],
        "counts": {
            "total": len(rows),
            "compaction": sum(r["kind"] == "compaction" for r in rows),
            "action": sum(r["kind"] == "action" for r in rows),
        },
        "cases": sorted({r["case_id"] for r in rows}),
        "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
    }
    args.manifest.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"holdout rows={len(rows)} "
          f"(compaction={manifest['counts']['compaction']} action={manifest['counts']['action']}) "
          f"-> {args.out}")
    print(f"sha256={manifest['sha256']}")


if __name__ == "__main__":
    main()
