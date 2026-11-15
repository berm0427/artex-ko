"""Additive v65: fix the PLANNER DUP-INTENT stall (diagnosed live #796). After a
capability/recon intent finishes with an OBSERVATION (Lua io/os/package=nil, a
version, a fingerprint), the planner must NOT re-propose the same probe — the
dedup guard rejects it and the task goes idle. Instead add_intent a DISTINCT next
step that BUILDS ON the observation: version-confirm → (if blocked) web_search →
alternative attack avenue. Additive on v64 (340). Planner rows on the real
6030-char planner system + planner toolset (static-done template)."""
import copy
import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent
BASE = ROOT / "agentic_v64_proto"
OUT = ROOT / "agentic_v65_proto"
SOURCE = "agentic_v65_diversify"

NOTE = ("coverage는 수집된 근거의 범위를 설명하는 대략적 추산치입니다. done 상태는 해당 "
        "의도의 기록 정리가 끝났음을 뜻하며 목표 전체 달성이나 추가 실행 검증 성공을 뜻하지 "
        "않습니다. 그래프에 없는 응답 본문이나 실행 결과는 알려진 것으로 취급하지 않습니다. "
        "목표 문장, 확인된 사실, 진행 중인 의도 유무를 함께 해석해야 합니다.")

CLOSER = ("\n\n위 현황을 바탕으로, 목표가 미달성이고 진행 중인 의도가 없으면 가장 가치 높은 "
          "새 방향 하나를 add_intent로 제출하세요. 직전 의도가 이미 관측한 사실을 다시 확인하는 "
          "의도는 동일 의도로 거부되므로, 그 관측을 전제로 한 '다음 단계'를 제시하세요. 프롬프트의 "
          "예시 값을 그대로 베끼지 말고 위 현황의 실제 대상에 집중하세요.")


def context(goal, facts, nodes):
    gov = {"coverage": {"by_type": [], "denominator": 0, "host_count": 0, "note": NOTE},
           "nodes": nodes}
    return ("방금 구체적인 변경이 있었습니다. 아래의 【이번 회차를 일으킨 실제 변경 사항】을 읽고 "
            "다음 단계를 계획하세요:\n\n【이번 회차를 일으킨 실제 변경 사항】\n"
            "이번 계획 갱신에 사용할 현황을 아래에 모았습니다.\n【현황】 목표: " + goal + "\n"
            "확인된 사실:\n" + "\n".join("- " + f for f in facts) + "\n"
            "open/running 의도: 없음(직전 의도 완료, 근거 기록됨).\n\n【graph_overview】 "
            + json.dumps(gov, ensure_ascii=False) + CLOSER)


def call(name, arguments):
    return ("<tool_call>\n" + json.dumps({"name": name, "arguments": arguments},
            ensure_ascii=False) + "\n</tool_call>")


def main():
    rows = [json.loads(l) for l in (BASE / "train.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    assert len(rows) == 340, len(rows)
    assert all(r["source"] == "agentic_v64_worker" for r in rows)

    tmpl = next(r for r in rows if r["sample_id"] == "static-done_v00")
    system = tmpl["messages"][0]["content"]
    assert len(system) > 4000, "planner template must carry the real long system"
    tools = copy.deepcopy(tmpl["tools"])
    tnames = [t["function"]["name"] for t in tools]
    assert "add_intent" in tnames

    for r in rows:
        prior = r.get("orig_source")
        r["orig_source"] = ({"source": r["source"], "orig_source": prior}
                            if prior is not None else r["source"])
        r["source"] = SOURCE

    added = []

    def add(slug, goal, facts, nodes, arguments, method, kind="action"):
        added.append({
            "case_id": f"v65-{slug}", "sample_id": f"v65-{slug}_v00", "source": SOURCE,
            "orig_source": {"synthetic": True, "method": method}, "kind": kind,
            "messages": [{"role": "system", "content": system},
                         {"role": "user", "content": context(goal, facts, nodes)}],
            "completion": call("add_intent", arguments), "tools": copy.deepcopy(tools),
        })

    # 1) Capability observed (io/os/package=nil) → DON'T re-probe; pivot to VERSION.
    add("cap-done-version-pivot",
        "로컬 Redis 취약 경로 재현",
        ["Lua EVAL 역량 점검에서 io/os/package가 nil로 관측됨(샌드박스 제약 정황). 같은 역량 재확인은 불필요",
         "대상 Redis의 실제 버전은 아직 미확인"],
        [{"id": 9101, "kind": "fact", "summary": "Lua io/os/package=nil"},
         {"id": 9102, "kind": "intent", "state": "done", "summary": "Lua io/os/package 역량 확인"}],
        {"summary": "io/os/package=nil은 이미 관측됐으니 같은 역량 EVAL을 재실행하지 말고, 관측된 제약에 맞는 공개 정보를 좁히기 위해 대상 Redis의 실제 버전을 EVAL(redis.call('INFO','server')) 또는 INFO server로 확인한다", "priority": 7},
        "planner_pivot_capability_to_version")

    # 2) Version blocked → DON'T retry version; pivot to web_search (via worker intent).
    add("version-blocked-search-pivot",
        "로컬 Redis 취약 경로 재현",
        ["io/os/package=nil 관측(샌드박스 제약)", "버전 확인(INFO server/EVAL INFO)이 -NOPERM으로 막힘"],
        [{"id": 9111, "kind": "fact", "summary": "INFO -NOPERM, 버전 미확인"},
         {"id": 9112, "kind": "intent", "state": "done", "summary": "버전 확인 시도(NOPERM)"}],
        {"summary": "버전 확인이 NOPERM으로 막혔으니 같은 확인을 반복하지 말고, 확인된 서비스(Redis)와 관측된 Lua 제약으로 worker가 공개 보안 권고문·완화책을 web_search해 필요한 익스플로잇 경로를 식별하도록 한다", "priority": 7},
        "planner_pivot_version_to_search")

    # 3) Sandbox hardened → DON'T re-probe Lua; pivot to an UNEXPLORED avenue.
    add("cap-done-altavenue-pivot",
        "로컬 Redis 취약 경로 재현",
        ["Lua 샌드박스가 io/os/package 제거로 하드닝됨 — Lua 재탐침은 소득 없음",
         "ACL 계정·권한, replication, module 적재는 아직 미점검"],
        [{"id": 9121, "kind": "intent", "state": "done", "summary": "Lua 샌드박스 역량 확인(하드닝)"}],
        {"summary": "Lua 경로가 막혔으니 같은 probe를 반복하지 말고, 아직 안 본 경로—ACL 계정·권한 열거, replication, module 적재 가능성—을 점검해 권한 상승 경로를 식별한다", "priority": 6},
        "planner_pivot_capability_to_altavenue")

    # 4) GENERAL web analogue: fingerprint done → DON'T re-fingerprint; pivot to research.
    add("general-web-fingerprint-pivot",
        "허가된 웹 대상 취약 경로 재현",
        ["엔드포인트 핑거프린트 완료(Server 헤더로 제품 nginx 확인, 버전은 숨김). 같은 핑거프린트 재확인 불필요"],
        [{"id": 9131, "kind": "intent", "state": "done", "summary": "핑거프린트·역량 확인(nginx, 버전 숨김)"}],
        {"summary": "핑거프린트는 끝났으니 같은 확인을 반복하지 말고, 확인된 제품(nginx)으로 worker가 공개 보안 권고문·설정 약점을 web_search하거나 아직 안 본 경로(인증·경로 우회)를 점검하도록 한다", "priority": 6},
        "planner_pivot_fingerprint_to_research_general")

    # 5) DECISION row: generalize — an observation-ending intent routes to the NEXT
    #    distinct step, never a re-confirmation (which dedup rejects → idle).
    added.append({
        "case_id": "v65-no-reprobe-decision", "sample_id": "v65-no-reprobe-decision_v00",
        "source": SOURCE, "orig_source": {"synthetic": True, "method": "decision_planner_diversify_next_intent"},
        "kind": "decision",
        "messages": [{"role": "system", "content": system},
                     {"role": "user", "content": "상황: 직전 의도가 역량·정찰을 '관측'으로 끝냈다(제약·버전·핑거프린트 확인). 다음 회차에서 플래너가 어떤 의도를 내야 하는지 원칙을 한 문장으로 정하라."}],
        "completion": ("직전 의도가 어떤 사실을 '관측'으로 끝냈으면 같은 관측을 재확인하는 의도는 동일 의도로 "
                       "거부되어 진행이 멈추므로 절대 내지 말고, 그 관측을 전제로 한 다음 단계—버전 확인, "
                       "막히면 공개 정보 web_search, 또는 아직 안 본 공격 경로—를 변경된 근거와 새 검증 질문이 "
                       "분명한 구별된 add_intent로 제출한다."),
        "tools": copy.deepcopy(tools),
    })

    all_rows = rows + added
    assert all(r["source"] == SOURCE for r in all_rows)
    action_added = [r for r in added if r["kind"] == "action"]
    assert all(r["completion"].startswith("<tool_call>\n") for r in action_added)
    assert all('"add_intent"' in r["completion"] for r in action_added)
    assert len(all_rows) == 345, len(all_rows)

    OUT.mkdir(exist_ok=True)
    (OUT / "train.jsonl").write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in all_rows), encoding="utf-8")
    counts = Counter(r["kind"] for r in all_rows)
    manifest = {"version": 1, "source": SOURCE,
                "counts": {"train": len(all_rows), **dict(counts)},
                "composition": {"v64_base": len(rows), "v65_diversify": len(added)},
                "training_intent": (
                    "epochs<=2; PLANNER must not re-propose an intent that only re-confirms an "
                    "already-observed fact (dedup rejects it → idle); after a capability/recon "
                    "observation, add_intent a DISTINCT next step that builds on it "
                    "(version-confirm → web_search when blocked → unexplored avenue); redis + "
                    "general web analogue + one decision row; evaluate on untouched holdout")}
    (OUT / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(len(all_rows), len(added), dict(counts))


if __name__ == "__main__":
    main()
