"""Additive v64: reinforce WORKER thoroughness WITHIN one intent + search-when-blocked,
grounded on the REAL ~9594-char worker system (v61/v62 worker rows used a 97-char stub).
#787 showed a worker that did one benign probe then concluded "추가 동작 없음" too early.
The worker's "finish one intent then stop" is by design, but when the intent asks for
deeper probing it must escalate within the intent, not quit after a single benign call;
and when a capability/version check is blocked it should web_search. Additive on v63 (335)."""
import copy
import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent
BASE = ROOT / "agentic_v63_proto"
OUT = ROOT / "agentic_v64_proto"
SOURCE = "agentic_v64_worker"

WEB_SEARCH = {
    "type": "function",
    "function": {
        "name": "web_search",
        "description": (
            "Search the web and return a ranked list of results (title, URL, "
            "description) — metadata only, up to 5 by default. Use WebFetch to read "
            "the full content of a specific URL. This makes an external network request."
        ),
        "parameters": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "The search query."},
                "limit": {"type": "integer", "description": "Max results (1-20). Default 5.",
                          "minimum": 1, "maximum": 20},
            },
            "required": ["query"],
        },
    },
}


def call(name, arguments):
    return ("<tool_call>\n" + json.dumps({"name": name, "arguments": arguments},
            ensure_ascii=False) + "\n</tool_call>")


def main():
    rows = [json.loads(l) for l in (BASE / "train.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    assert len(rows) == 335, len(rows)
    assert all(r["source"] == "agentic_v63_pivot" for r in rows)

    # Real worker template: 9594-char worker system + real worker toolset (tcp_session, etc.).
    tmpl = next(r for r in rows if r["sample_id"] == "redis-worker-proto_v00")
    system = tmpl["messages"][0]["content"]
    assert len(system) > 4000, "worker template must carry the real long worker system"
    base_tools = copy.deepcopy(tmpl["tools"])
    tnames = [t["function"]["name"] for t in base_tools]
    assert "tcp_session" in tnames and "record_fact" in tnames

    def tools_with_search():
        t = copy.deepcopy(base_tools)
        if not any(x["function"]["name"] == "web_search" for x in t):
            t.append(copy.deepcopy(WEB_SEARCH))
        return t

    for r in rows:
        prior = r.get("orig_source")
        r["orig_source"] = ({"source": r["source"], "orig_source": prior}
                            if prior is not None else r["source"])
        r["source"] = SOURCE

    added = []

    def add(slug, user, name, arguments, method, tools, kind="action"):
        added.append({
            "case_id": f"v64-{slug}", "sample_id": f"v64-{slug}_v00", "source": SOURCE,
            "orig_source": {"synthetic": True, "method": method}, "kind": kind,
            "messages": [{"role": "system", "content": system},
                         {"role": "user", "content": user}],
            "completion": call(name, arguments), "tools": copy.deepcopy(tools),
        })

    # 1) THE #787 fix: benign probe confirmed the exec path WITHIN this intent — do not
    #    conclude "추가 동작 없음"; escalate to the capability probe the intent asks for.
    add("escalate-within-intent",
        "의도: 대상 Redis의 Lua 샌드박스 역량을 확인하라. 이번 세션에서 benign EVAL "
        "`return redis.call('TIME')`가 정상 RESP를 반환해 Lua 실행 경로가 동작함을 확인했다. "
        "이 benign 결과만으로 '추가 동작 없음'으로 종결하지 말고, 의도가 요구하는 역량 확인을 "
        "수행하라. io/os/package/loadstring/cjson 존재 여부를 읽기 전용으로 한 번에 점검하되 "
        "라이브러리를 호출하지 말고 존재 여부(type)만 보고하라.",
        "tcp_session",
        {"action": "send", "resp_args": ["EVAL",
         "return {type(rawget(_G,'io')),type(rawget(_G,'os')),type(rawget(_G,'package')),type(rawget(_G,'loadstring')),type(cjson)}",
         "0"]},
        "worker_escalate_after_benign_within_intent", base_tools)

    # 2) GENERAL analogue: a benign health check passed within a broader intent — go
    #    deeper on the same intent instead of stopping at the benign result.
    add("general-service-deepen",
        "의도: 허가된 웹 서비스의 접근 제어 약점을 점검하라. benign `GET /health`가 200을 "
        "반환해 서비스가 살아 있음을 확인했다. 이 헬스체크로 멈추지 말고, 의도가 가리키는 "
        "접근 제어 점검을 이어가라. 인증 없이 보호 자원에 접근되는지 확인하는 다음 요청을 보내라.",
        "tcp_session",
        {"action": "send_text", "text": "GET /admin/config HTTP/1.1\r\nHost: target\r\nConnection: close\r\n\r\n"},
        "worker_deepen_after_benign_general", base_tools)

    # 3) When a capability/version check inside this intent is BLOCKED, search public
    #    advisories (real worker system this time) instead of re-probing or stopping.
    add("blocked-then-search",
        "의도: 관측된 Lua 샌드박스 제약에 적용되는 공개 권고문을 조사하라. 역량 EVAL에서 "
        "io/os/package가 nil로 관찰됐고, 버전 확인(INFO server)은 -NOPERM으로 막혔다. 같은 "
        "확인을 반복하거나 멈추지 말고, 확인된 서비스(Redis)와 관찰된 제약으로 공개 보안 "
        "권고문을 버전 불명 상태 그대로 검색하라. 검색 결과를 플래그 증거로 취급하지 마라.",
        "web_search",
        {"query": "Redis Lua scripting sandbox restricted globals security advisories mitigation"},
        "worker_search_when_blocked_real_system", tools_with_search())

    # 4) record_fact framing: a benign confirmation is a MILESTONE, not goal completion —
    #    keeps the worker from over-claiming/closing out on a benign result.
    add("benign-is-milestone",
        "의도: 대상 서비스의 실행 경로를 확인하고 심화 탐색을 준비하라. benign 호출이 성공해 "
        "실행 경로가 동작함을 확인했다. 이 사실을 기록하되, 목표 달성이나 취약점 확정으로 "
        "과장하지 말고 아직 심화 탐색이 남았음을 명시하라.",
        "record_fact",
        {"intent_id": 8820, "confidence": "observed",
         "summary": "benign 호출로 실행 경로 동작을 확인(마일스톤)했을 뿐, 목표 미획득·취약점 미확정이며 역량/권한 심화 탐색이 남아 있다",
         "detail": "benign 결과는 실행 경로의 존재만 보이며, 플래그·권한 상승·민감 자원 접근을 뜻하지 않는다. 다음 단계로 역량·권한 경계를 점검한다.",
         "evidence_quote": ""},
        "worker_benign_milestone_not_done", base_tools)

    # 5) DECISION row: generalize — if the intent asks for deeper probing, a single
    #    benign call is not completion; exhaust the intent's depth before stopping.
    added.append({
        "case_id": "v64-worker-noquit-decision", "sample_id": "v64-worker-noquit-decision_v00",
        "source": SOURCE, "orig_source": {"synthetic": True, "method": "decision_worker_exhaust_intent_depth"},
        "kind": "decision",
        "messages": [{"role": "system", "content": system},
                     {"role": "user", "content": "상황: 领到的 의도가 '심화 탐색/역량 확인'을 요구하는데 benign 확인 한 번만 하고 멈출지 고민된다. 원칙을 한 문장으로 정하라."}],
        "completion": ("의도가 심화 탐색·역량 확인을 요구하면 benign 확인 한 번은 완료가 아니라 시작점이므로, "
                       "같은 benign을 반복하지 말고 의도가 가리키는 다음 단계(역량·권한·버전 점검, 막히면 "
                       "공개 정보 검색)를 수행해 의도의 깊이를 소진한 뒤에 발견을 기록하고 정지한다. "
                       "실제 관측으로 근거가 확인된 것만 기록하고 benign 성공을 목표 달성으로 과장하지 않는다."),
        "tools": copy.deepcopy(base_tools),
    })

    all_rows = rows + added
    assert all(r["source"] == SOURCE for r in all_rows)
    action_added = [r for r in added if r["kind"] == "action"]
    assert all(r["completion"].startswith("<tool_call>\n") for r in action_added)
    assert len(all_rows) == 340, len(all_rows)

    OUT.mkdir(exist_ok=True)
    (OUT / "train.jsonl").write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in all_rows), encoding="utf-8")
    counts = Counter(r["kind"] for r in all_rows)
    manifest = {"version": 1, "source": SOURCE,
                "counts": {"train": len(all_rows), **dict(counts)},
                "composition": {"v63_base": len(rows), "v64_worker_depth": len(added)},
                "training_intent": (
                    "epochs<=2; WORKER on the real ~9594-char worker system: when the assigned "
                    "intent asks for deeper probing, a single benign call is NOT completion — "
                    "escalate within the intent (io/os capability probe, deeper request), "
                    "web_search when a check is blocked, frame benign success as a milestone "
                    "(not goal completion), exhaust the intent's depth before stopping; redis + "
                    "general web analogue + milestone record_fact + decision row; untouched holdout")}
    (OUT / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(len(all_rows), len(added), dict(counts))


if __name__ == "__main__":
    main()
