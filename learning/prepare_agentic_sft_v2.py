#!/usr/bin/env python3
"""agentic SFT v2 데이터셋 생성기 — compaction 다양성·비반복 분석 보강.

왜
--
v33 holdout 평가에서 v1(9개)로 학습한 어댑터는 action(add_intent)은 개선했으나
compaction에서 <analysis> 서문을 통째로 반복하는 루프가 생겼다(엄격 지표 1/3).
rank/epoch를 줄여도 루프 위치만 옮겨갈 뿐 사라지지 않았다. 원인 가설: 학습
compaction 예제가 3개뿐이고 분석 구조가 단조로워, 모델이 "같은 문단을 다시
쓰는" 스타일을 배웠다.

이 생성기는 그 가설을 시험할 v2 데이터셋을 만든다:
  - v1의 9개(compaction 3 + action 6)를 **내용 그대로** 보존하되 source만 v2로
    재스탬프한다(로더의 단일 source 불변식 유지).
  - 분석 블록을 "서로 다른 2~3문장, 문단 반복 없음"으로 쓴 compaction 예제를
    더 넣는다. 도메인·길이를 분산해 한 가지 틀을 외우지 않게 한다.
action 예제는 이미 잘 되므로 늘리지 않는다.

안전: 모두 허가된 격리 로컬 환경의 정적 정찰 수준. flag 값·목표 문자열·동작
익스플로잇을 넣지 않는다.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

V1_PATH = Path("agentic_sft_v1/train.jsonl")
SOURCE_V2 = "synthetic_agentic_v2"

COMPACTION_SYSTEM = "You are a helpful AI assistant tasked with summarizing conversations."
# train.jsonl의 두 번째 user 메시지(지시문)와 동일하게 유지한다.
COMPACTION_INSTRUCTION = (
    "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools. Your entire response "
    "must be an <analysis> block followed by a <summary> block.\n"
    "Create a detailed summary of the conversation so far, capturing technical "
    "details needed to continue without losing context. The <summary> must have "
    "these sections: 1. Primary Request and Intent, 2. Key Technical Concepts, "
    "3. Files and Code Sections, 4. Errors and fixes, 5. Problem Solving, "
    "6. All user messages, 7. Pending Tasks, 8. Current Work, 9. Optional Next Step."
)

# 새 compaction 예제. analysis는 짧고 서로 다른 문장들(반복 금지), summary는 9섹션.
NEW_COMPACTION = [
    {
        "case_id": "compact-perm-audit",
        "transcript": [
            "USER: 격리된 로컬 서버의 파일 권한 설정을 점검해 과도한 쓰기 권한을 찾아라. 변경 금지, 읽기만.",
            "ASSISTANT: 목표를 '세계 쓰기 가능(world-writable) 경로와 setuid 파일 식별'로 설정.",
            "ASSISTANT(tool Bash find): /opt/app 아래에 world-writable 디렉터리 2곳 확인.",
            "ASSISTANT(tool Bash ls): config.d/가 777, 소유자는 root.",
            "ASSISTANT: 다음은 setuid 바이너리 목록을 뽑아 권한 상승 표면을 좁힐 계획.",
        ],
        "analysis": [
            "읽기 전용 권한 점검이며 변경은 금지된다.",
            "지금까지 world-writable 디렉터리 2곳(config.d는 777/root)을 찾았다.",
            "남은 일은 setuid 목록 확보로, 이는 권한 상승 표면을 좁히는 데 필요하다.",
        ],
        "summary": {
            "1. Primary Request and Intent": "격리된 로컬 서버의 파일 권한을 읽기 전용으로 점검해 과도한 쓰기 권한을 찾는다. 변경 금지.",
            "2. Key Technical Concepts": "world-writable 권한, setuid 비트, 소유자/모드, 권한 상승 표면.",
            "3. Files and Code Sections": "/opt/app/config.d(모드 777, 소유자 root), world-writable 디렉터리 2곳.",
            "4. Errors and fixes": "없음.",
            "5. Problem Solving": "find로 world-writable 경로를 모으고 ls로 모드·소유자를 확인했다.",
            "6. All user messages": "로컬 서버 파일 권한을 읽기만으로 점검해 과도한 쓰기 권한을 찾아라.",
            "7. Pending Tasks": "setuid 바이너리 목록 수집, 상승 표면 정리.",
            "8. Current Work": "config.d 777 확인 직후, setuid 목록 작성 직전.",
            "9. Optional Next Step": "find / -perm -4000으로 setuid 바이너리를 나열한다.",
        },
    },
    {
        "case_id": "compact-log-triage",
        "transcript": [
            "USER: 제공된 로컬 접근 로그에서 비정상 요청 패턴만 추려라. 외부 전송 금지.",
            "ASSISTANT: 목표를 '단일 IP의 고빈도 404 폭주 구간 식별'로 설정.",
            "ASSISTANT(tool Bash awk): 상위 IP 집계에서 10.0.0.5가 전체의 62%.",
            "ASSISTANT(tool Bash grep): 10.0.0.5 요청의 대부분이 /admin 경로 404.",
            "ASSISTANT: 다음은 시간대별 분포로 자동화 스캔 여부를 가릴 계획.",
        ],
        "analysis": [
            "로컬 로그 분석이며 결과의 외부 전송은 금지된다.",
            "10.0.0.5가 트래픽의 62%를 차지하고 대부분 /admin 404였다.",
            "시간 분포를 보면 사람이 아닌 자동 스캔인지 판별할 수 있다.",
        ],
        "summary": {
            "1. Primary Request and Intent": "로컬 접근 로그에서 비정상 요청 패턴만 추린다. 외부 전송 금지.",
            "2. Key Technical Concepts": "요청 집계, 404 비율, 단일 출처 집중, 자동화 스캔 징후.",
            "3. Files and Code Sections": "접근 로그(출처 IP·경로·상태코드 필드).",
            "4. Errors and fixes": "없음.",
            "5. Problem Solving": "awk로 IP를 집계하고 grep으로 10.0.0.5의 /admin 404 집중을 확인했다.",
            "6. All user messages": "로컬 접근 로그에서 비정상 요청 패턴만 추려라, 외부 전송 금지.",
            "7. Pending Tasks": "시간대별 분포 산출로 자동 스캔 여부 판정.",
            "8. Current Work": "출처·경로 집중 확인 직후, 시간 분포 산출 직전.",
            "9. Optional Next Step": "10.0.0.5 요청을 분 단위로 버킷팅해 간격 규칙성을 본다.",
        },
    },
    {
        "case_id": "compact-config-audit",
        "transcript": [
            "USER: 로컬 서비스의 설정 파일에서 안전하지 않은 기본값을 찾아라. 값 변경은 하지 마라.",
            "ASSISTANT: 목표를 '디버그 노출·약한 암호화 기본값 식별'로 설정.",
            "ASSISTANT(tool Read settings.ini): debug=true, secret_key가 예시 기본값 그대로.",
            "ASSISTANT: 다음은 세션·쿠키 관련 플래그를 확인할 계획.",
        ],
        "analysis": [
            "설정 파일의 안전하지 않은 기본값을 찾는 읽기 전용 점검이다.",
            "debug=true와 예시 그대로의 secret_key 두 가지가 이미 드러났다.",
            "남은 확인 대상은 세션·쿠키 보안 플래그다.",
        ],
        "summary": {
            "1. Primary Request and Intent": "로컬 서비스 설정 파일에서 안전하지 않은 기본값을 찾는다. 값 변경 금지.",
            "2. Key Technical Concepts": "디버그 노출, 하드코딩된 예시 비밀값, 세션/쿠키 보안 플래그.",
            "3. Files and Code Sections": "settings.ini(debug=true, secret_key=예시 기본값).",
            "4. Errors and fixes": "없음.",
            "5. Problem Solving": "settings.ini를 읽어 디버그 활성화와 기본 secret_key를 확인했다.",
            "6. All user messages": "로컬 서비스 설정에서 안전하지 않은 기본값을 찾아라, 값 변경 금지.",
            "7. Pending Tasks": "세션·쿠키 보안 플래그 확인, 발견 목록 정리.",
            "8. Current Work": "debug·secret_key 확인 직후, 세션 플래그 확인 직전.",
            "9. Optional Next Step": "settings.ini의 session_secure·cookie_httponly 값을 읽는다.",
        },
    },
    {
        "case_id": "compact-container-inspect",
        "transcript": [
            "USER: 제공된 로컬 컨테이너 이미지의 구성을 정적으로 살펴 노출 위험을 찾아라. 실행하지 마라.",
            "ASSISTANT: 목표를 'Dockerfile의 루트 실행·불필요 포트 노출 식별'로 설정.",
            "ASSISTANT(tool Read Dockerfile): USER 지시 없음(root로 실행), EXPOSE 22 포함.",
            "ASSISTANT: 다음은 복사되는 파일 중 비밀이 들어갈 만한 경로를 확인할 계획.",
        ],
        "analysis": [
            "컨테이너 이미지를 실행 없이 정적으로만 살핀다.",
            "Dockerfile이 root로 실행되고 22번 포트를 노출하는 두 위험이 보인다.",
            "다음은 COPY 대상 중 비밀 유입 경로 점검이다.",
        ],
        "summary": {
            "1. Primary Request and Intent": "로컬 컨테이너 이미지를 정적으로 살펴 노출 위험을 찾는다. 실행 금지.",
            "2. Key Technical Concepts": "비루트 실행 부재, 포트 노출, 이미지 레이어의 비밀 유입.",
            "3. Files and Code Sections": "Dockerfile(USER 미지정→root, EXPOSE 22).",
            "4. Errors and fixes": "없음.",
            "5. Problem Solving": "Dockerfile을 읽어 root 실행과 22 포트 노출을 확인했다.",
            "6. All user messages": "로컬 컨테이너 이미지 구성을 정적으로 살펴 노출 위험을 찾아라, 실행 금지.",
            "7. Pending Tasks": "COPY 대상 경로의 비밀 유입 여부 확인.",
            "8. Current Work": "root 실행·포트 노출 확인 직후, COPY 경로 점검 직전.",
            "9. Optional Next Step": "Dockerfile의 COPY/ADD 대상에 키·토큰 파일이 포함되는지 확인한다.",
        },
    },
    {
        "case_id": "compact-api-route-recon",
        "transcript": [
            "USER: 로컬 API 서버의 라우트 정의를 정적으로 읽어 인증 없이 닿는 경로를 찾아라. 요청은 보내지 마라.",
            "ASSISTANT: 목표를 '인증 미들웨어가 안 걸린 라우트 식별'로 설정.",
            "ASSISTANT(tool Read routes.py): /health와 /metrics가 인증 데코레이터 없이 등록됨.",
            "ASSISTANT(tool Read auth.py): 데코레이터가 핸들러별 수동 적용 방식.",
            "ASSISTANT: 다음은 각 라우트에 데코레이터 적용 여부를 전수 대조할 계획.",
        ],
        "analysis": [
            "요청을 보내지 않고 라우트 정의만 정적으로 읽는다.",
            "/health와 /metrics가 인증 데코레이터 없이 등록돼 있었다.",
            "인증이 핸들러별 수동 적용이라, 전수 대조로 누락을 찾아야 한다.",
        ],
        "summary": {
            "1. Primary Request and Intent": "로컬 API 라우트를 정적으로 읽어 인증 없이 닿는 경로를 찾는다. 요청 전송 금지.",
            "2. Key Technical Concepts": "라우트 등록, 인증 데코레이터, 핸들러별 수동 적용, 누락 탐지.",
            "3. Files and Code Sections": "routes.py(/health·/metrics 데코레이터 없음), auth.py(수동 적용).",
            "4. Errors and fixes": "없음.",
            "5. Problem Solving": "routes.py와 auth.py를 읽어 인증 미적용 라우트 두 개를 확인했다.",
            "6. All user messages": "로컬 API 라우트를 정적으로 읽어 인증 없이 닿는 경로를 찾아라, 요청 금지.",
            "7. Pending Tasks": "전체 라우트에 데코레이터 적용 여부 전수 대조.",
            "8. Current Work": "미적용 라우트 2개 확인 직후, 전수 대조 직전.",
            "9. Optional Next Step": "routes.py의 모든 등록 항목과 auth.py 적용 목록을 1:1로 대조한다.",
        },
    },
]


def transcript_user(lines: list[str]) -> str:
    return "아래는 지금까지의 대화입니다.\n\n" + "\n".join(lines)


def completion_text(analysis: list[str], summary: dict) -> str:
    body = "\n".join(f"{k}: {v}" for k, v in summary.items())
    return "<analysis>\n" + "\n".join(analysis) + "\n</analysis>\n<summary>\n" + body + "\n</summary>"


def new_compaction_row(case: dict) -> dict:
    return {
        "case_id": case["case_id"],
        "sample_id": case["case_id"],
        "source": SOURCE_V2,
        "kind": "compaction",
        "messages": [
            {"role": "system", "content": COMPACTION_SYSTEM},
            {"role": "user", "content": transcript_user(case["transcript"])},
            {"role": "user", "content": COMPACTION_INSTRUCTION},
        ],
        "completion": completion_text(case["analysis"], case["summary"]),
    }


def load_v1_restamped() -> list[dict]:
    rows = [json.loads(l) for l in V1_PATH.read_text(encoding="utf-8").splitlines() if l.strip()]
    for r in rows:
        r["source"] = SOURCE_V2  # 내용은 그대로, 출처만 v2로.
    return rows


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--out", type=Path, default=Path("agentic_sft_v2/train.jsonl"))
    ap.add_argument("--manifest", type=Path, default=Path("agentic_sft_v2/manifest.json"))
    args = ap.parse_args()

    rows = load_v1_restamped() + [new_compaction_row(c) for c in NEW_COMPACTION]
    ids = [r.get("sample_id") or r["case_id"] for r in rows]
    if len(set(ids)) != len(rows):
        raise SystemExit("duplicate sample id across v1 and new rows")

    args.out.parent.mkdir(parents=True, exist_ok=True)
    text = "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in rows)
    args.out.write_text(text, encoding="utf-8")

    manifest = {
        "version": 1,
        "source": SOURCE_V2,
        "derived_from": "synthetic_agentic_v1",
        "purpose": "v1 plus varied non-repetitive compaction to reduce analysis-block looping",
        "counts": {
            "train": len(rows),
            "compaction": sum(r["kind"] == "compaction" for r in rows),
            "action": sum(r["kind"] == "action" for r in rows),
        },
        "rules": ["no evaluation-set items", "no flag values",
                  "compaction analysis must be short and non-repetitive",
                  "short examples (<=768 tokens target)"],
        "files": {"train.jsonl": hashlib.sha256(text.encode("utf-8")).hexdigest()},
    }
    args.manifest.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"v2 rows={len(rows)} (compaction={manifest['counts']['compaction']} "
          f"action={manifest['counts']['action']}) -> {args.out}")
    print(f"sha256={manifest['files']['train.jsonl']}")


if __name__ == "__main__":
    main()
