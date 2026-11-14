"""Additive v63: fix the PLANNER-IDLE dead-end (diagnosed live #786/#787). When an
intent finishes without progress and the goal is unmet, the planner must NOT return
empty/idle — it must add_intent for a PIVOT (direct the worker to web_search the
blocked version's advisory, or probe an unexplored avenue), or formally conclude.
Additive on v62 (330 rows). Planner rows use the real 6030-char system + planner
toolset from the static-done template."""
import copy
import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent
BASE = ROOT / "agentic_v62_proto"
OUT = ROOT / "agentic_v63_proto"
SOURCE = "agentic_v63_pivot"

# A compact but representative graph_overview note (the real rows carry a long
# repeated boilerplate; the model has seen many variants — a shorter faithful one
# keeps method-not-memorization and avoids copying one exact blob).
NOTE = ("coverage는 수집된 근거의 범위를 설명하는 대략적 추산치입니다. done 상태는 해당 "
        "의도의 기록 정리가 끝났음을 뜻하며 목표 전체 달성이나 추가 실행 검증 성공을 뜻하지 "
        "않습니다. 그래프에 없는 응답 본문이나 실행 결과는 알려진 것으로 취급하지 않습니다. "
        "목표 문장, 확인된 사실, 진행 중인 의도 유무를 함께 해석해야 합니다.")

CLOSER = ("\n\n위 현황을 바탕으로, 목표가 미달성이고 진행 중인 의도가 없으면 가장 가치 높은 "
          "새 방향 하나를 add_intent로 제출하세요. 같은 관측을 반복하거나 빈손으로 멈추지 말고, "
          "막힌 지점은 다른 경로나 공개 정보 조사로 우회하세요. 프롬프트의 예시 값을 그대로 "
          "베끼지 말고 위 현황의 실제 대상에 집중하세요.")


def context(goal, facts, nodes):
    gov = {"coverage": {"by_type": [], "denominator": 0, "host_count": 0, "note": NOTE},
           "nodes": nodes}
    body = ("방금 구체적인 변경이 있었습니다. 아래의 【이번 회차를 일으킨 실제 변경 사항】을 읽고 "
            "다음 단계를 계획하세요:\n\n【이번 회차를 일으킨 실제 변경 사항】\n"
            "이번 계획 갱신에 사용할 현황을 아래에 모았습니다.\n【현황】 목표: " + goal + "\n"
            "확인된 사실:\n" + "\n".join("- " + f for f in facts) + "\n"
            "open/running 의도: 없음(직전 의도 완료, 근거 기록됨).\n\n【graph_overview】 "
            + json.dumps(gov, ensure_ascii=False) + CLOSER)
    return body


def call(name, arguments):
    return ("<tool_call>\n" + json.dumps({"name": name, "arguments": arguments},
            ensure_ascii=False) + "\n</tool_call>")


def main():
    rows = [json.loads(l) for l in (BASE / "train.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    assert len(rows) == 330, len(rows)
    assert all(r["source"] == "agentic_v62_pivot" for r in rows)

    tmpl = next(r for r in rows if r["sample_id"] == "static-done_v00")
    assert tmpl["kind"] == "action"
    system = tmpl["messages"][0]["content"]
    assert len(system) > 4000, "planner template must carry the real long system"
    tools = copy.deepcopy(tmpl["tools"])
    tnames = [t["function"]["name"] for t in tools]
    assert "add_intent" in tnames and "prove_goal" in tnames

    for r in rows:
        prior = r.get("orig_source")
        r["orig_source"] = ({"source": r["source"], "orig_source": prior}
                            if prior is not None else r["source"])
        r["source"] = SOURCE

    added = []

    def add(slug, goal, facts, nodes, name, arguments, method, kind="action"):
        added.append({
            "case_id": f"v63-{slug}", "sample_id": f"v63-{slug}_v00", "source": SOURCE,
            "orig_source": {"synthetic": True, "method": method}, "kind": kind,
            "messages": [{"role": "system", "content": system},
                         {"role": "user", "content": context(goal, facts, nodes)}],
            "completion": call(name, arguments), "tools": copy.deepcopy(tools),
        })

    # 1) THE fix: version-confirm blocked -> pivot the worker to web_search (planner
    #    has no web_search itself; it DIRECTS the worker via add_intent). Never idle.
    add("redis-blocked-pivot-search",
        "로컬 Redis 취약 경로 재현",
        ["127.0.0.1:6381 benign EVAL 실행 확인(PONG), Lua io/os/package=nil 관측",
         "INFO server는 -NOPERM으로 버전 확인 불가"],
        [{"id": 7101, "kind": "fact", "summary": "Lua io/os/package=nil"},
         {"id": 7102, "kind": "intent", "state": "done", "summary": "역량 probe 및 버전 확인 시도 완료"}],
        "add_intent",
        {"summary": "버전이 NOPERM으로 막혔으니, 확인된 서비스(Redis)와 관측된 Lua 제약으로 공개 보안 권고문을 web_search로 조사하는 의도를 수행한다(버전 불명 상태 그대로, 반복 금지)", "priority": 7},
        "planner_pivot_to_search_when_version_blocked")

    # 2) Dead-end on benign recon -> add_intent for an UNEXPLORED avenue, not idle.
    add("redis-deadend-altavenue",
        "로컬 Redis 취약 경로 재현",
        ["benign EVAL(TIME/GET)만 수행 후 '추가 동작 없음'으로 종결",
         "ACL 계정/권한, replication, module 적재는 아직 미점검"],
        [{"id": 7111, "kind": "intent", "state": "done", "summary": "benign Lua 읽기 테스트, 이미 보고됨"}],
        "add_intent",
        {"summary": "benign 테스트에서 멈추지 말고 아직 안 본 경로—ACL 계정·권한 열거와 replication/module 적재 가능성—을 점검해 권한 상승 경로를 식별하는 의도를 수행한다", "priority": 6},
        "planner_pivot_to_unexplored_avenue")

    # 3) GENERAL web analogue: product known, version hidden, benign probe exhausted
    #    -> pivot to advisory research, not idle.
    add("general-web-deadend-pivot",
        "허가된 웹 대상 취약 경로 재현",
        ["Server 헤더로 제품은 확인(nginx)되나 버전은 숨겨짐",
         "benign GET 요청으로 추가 버전 단서 없음, 의도 완료"],
        [{"id": 7121, "kind": "intent", "state": "done", "summary": "헤더/핑거프린트 정리, 추가 단서 없음"}],
        "add_intent",
        {"summary": "버전이 숨겨졌으니 반복 요청 대신, 확인된 제품(nginx)과 관측된 동작으로 공개 보안 권고문·설정 약점을 web_search로 조사하는 의도를 수행한다", "priority": 6},
        "planner_pivot_general_web")

    # 4) GENERAL ssh analogue: banner suppressed, recon done -> pivot or widen, not idle.
    add("general-ssh-deadend-pivot",
        "허가된 시험 서비스 취약 경로 재현",
        ["SSH 배너가 억제되어 제품/버전 미노출",
         "배너 재수신으로 추가 정보 없음, 의도 완료"],
        [{"id": 7131, "kind": "intent", "state": "done", "summary": "배너/프로토콜 정리, 버전 미확인"}],
        "add_intent",
        {"summary": "배너를 다시 읽지 말고, 확인된 프로토콜(SSH)과 서비스 부류로 해당 SSH 구현의 공개 보안 지침을 web_search로 조사하거나 아직 안 본 인증 경로를 점검하는 의도를 수행한다", "priority": 6},
        "planner_pivot_general_ssh")

    # 5) DECISION row: the general rule — a live open goal + a dead-ended intent must
    #    route to a pivoting add_intent or a formal conclusion, never to empty/idle.
    added.append({
        "case_id": "v63-planner-noidle-decision", "sample_id": "v63-planner-noidle-decision_v00",
        "source": SOURCE, "orig_source": {"synthetic": True, "method": "decision_planner_never_idle"},
        "kind": "decision",
        "messages": [{"role": "system", "content": system},
                     {"role": "user", "content": "상황: 목표가 미달성인데 직전 의도가 성과 없이 완료되고 진행 중인 의도가 없다. 다음 회차에서 플래너가 지켜야 할 원칙을 한 문장으로 정하라."}],
        "completion": ("직전 의도가 성과 없이 끝났더라도 목표가 열려 있으면 빈손으로 멈추거나 같은 관측을 "
                       "반복하게 하지 말고, 아직 안 본 공격 경로를 겨냥한 add_intent를 내거나 막힌 지점을 "
                       "공개 정보 조사(worker web_search)로 우회하는 add_intent를 낸다. 실제 증거로 달성이 "
                       "입증된 목표가 있을 때만 prove_goal로 표시하며, 모든 경로가 소진되면 사실을 날조하지 "
                       "말고 남은 검증 방향이 없다는 판단과 근거를 기록한 뒤 새 의도 생성을 멈춘다."),
        "tools": copy.deepcopy(tools),
    })

    all_rows = rows + added
    assert all(r["source"] == SOURCE for r in all_rows)
    action_added = [r for r in added if r["kind"] == "action"]
    assert all(r["completion"].startswith("<tool_call>\n") for r in action_added)
    assert all('"add_intent"' in r["completion"] for r in action_added)
    assert len(all_rows) == 335, len(all_rows)

    OUT.mkdir(exist_ok=True)
    (OUT / "train.jsonl").write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in all_rows), encoding="utf-8")
    counts = Counter(r["kind"] for r in all_rows)
    manifest = {"version": 1, "source": SOURCE,
                "counts": {"train": len(all_rows), **dict(counts)},
                "composition": {"v62_base": len(rows), "v63_planner_noidle": len(added)},
                "training_intent": (
                    "epochs<=2; PLANNER must not return empty/idle on a live open goal after a "
                    "dead-ended intent — add_intent to pivot (direct worker to web_search the "
                    "blocked version's advisory, or probe an unexplored avenue), or prove_goal to "
                    "formally conclude only when evidence is exhausted; redis + general web/ssh "
                    "analogues + one planner decision row; evaluate on untouched holdout")}
    (OUT / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(len(all_rows), len(added), dict(counts))


if __name__ == "__main__":
    main()
