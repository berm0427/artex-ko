"""Additive v61: escalate past a benign EVAL to a capability/version probe,
then research — so the worker stops repeating the benign probe. Additive on v60."""
import copy
import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent
BASE = ROOT / "agentic_v60_proto"
OUT = ROOT / "agentic_v61_proto"
SOURCE = "agentic_v61_pivot"

WEB_SEARCH = {
    "type": "function",
    "function": {
        "name": "web_search",
        "description": (
            "Search the web and return a ranked list of results (title, URL, "
            "description) — metadata only, up to 5 by default. Use WebFetch "
            "to read the full content of a specific URL. This makes an "
            "external network request."
        ),
        "parameters": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "The search query."},
                "limit": {
                    "type": "integer",
                    "description": "Maximum number of results to return (1-20). Default 5.",
                    "minimum": 1,
                    "maximum": 20,
                },
            },
            "required": ["query"],
        },
    },
}


def call(name, arguments):
    return (
        "<tool_call>\n"
        + json.dumps({"name": name, "arguments": arguments}, ensure_ascii=False)
        + "\n</tool_call>"
    )


def main():
    rows = [
        json.loads(line)
        for line in (BASE / "train.jsonl").read_text(encoding="utf-8").splitlines()
        if line.strip()
    ]
    assert len(rows) == 320
    assert all(row["source"] == "agentic_v60_pivot" for row in rows)

    template = next(
        row for row in rows if row["sample_id"] == "redis781-parser-framing-correction"
    )
    assert template["kind"] == "action"
    assert any(
        t["function"]["name"] == "tcp_session"
        and "resp_args" in t["function"]["parameters"]["properties"]
        for t in template["tools"]
    )
    system = template["messages"][0]["content"]
    tools = copy.deepcopy(template["tools"])
    if not any(t["function"]["name"] == "web_search" for t in tools):
        tools.append(copy.deepcopy(WEB_SEARCH))
    assert [t["function"]["name"] for t in tools].count("web_search") == 1

    for row in rows:
        prior = row.get("orig_source")
        row["orig_source"] = (
            {"source": row["source"], "orig_source": prior}
            if prior is not None else row["source"]
        )
        row["source"] = SOURCE

    added = []

    def add(slug, history, name, arguments, method):
        added.append({
            "case_id": f"v61-{slug}",
            "sample_id": f"v61-{slug}_v00",
            "source": SOURCE,
            "orig_source": {"synthetic": True, "method": method},
            "kind": "action",
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": history},
            ],
            "completion": call(name, arguments),
            "tools": copy.deepcopy(tools),
        })

    # 1) Escalate past the benign confirm to a ONE-SHOT capability probe. This is
    #    the row that breaks the "repeat TIME forever" loop: it produces the
    #    nil observation the v60 search rows already act on. Reports presence
    #    (type) only; never invokes io/os.
    add(
        "cap-escalate",
        "127.0.0.1:6381에서 benign EVAL `return redis.call('TIME')`가 정상 RESP 배열을 "
        "반환해 Lua 실행 경로가 동작함을 확인했다. 같은 benign 호출을 반복하지 마라. "
        "샌드박스가 io/os/package/loadstring/cjson를 제거했는지 한 번에 확인하는 "
        "읽기 전용 EVAL로 에스컬레이션하라. 라이브러리를 호출하지 말고 존재 여부(type)만 보고하라. "
        "출력이 비었거나 모호하면 제약을 단정하지 말고 다시 확인하라.",
        "tcp_session",
        {"action": "send", "resp_args": [
            "EVAL",
            "return {type(rawget(_G,'io')),type(rawget(_G,'os')),type(rawget(_G,'package')),type(rawget(_G,'loadstring')),type(cjson)}",
            "0",
        ]},
        "escalate_capability_probe_after_benign_confirm",
    )

    # 2) On an OBSERVED restriction (nil), confirm the target version FIRST so the
    #    later search is narrow (omniroute: a pre-version search is too broad),
    #    instead of repeating the probe.
    add(
        "version-confirm",
        "역량 EVAL의 원시 응답에서 io/os/package가 nil로 관찰됐다(샌드박스 제약 정황). "
        "바로 검색하지 말고, 먼저 대상의 실제 버전을 INFO server로 확인해 이후 검색을 "
        "그 버전으로 좁혀라.",
        "tcp_session",
        {"action": "send", "resp_args": ["INFO", "server"]},
        "confirm_version_before_search",
    )

    # 3) Version-AWARE research once the version is known. Not a flag; evidence
    #    stays the target's raw response.
    add(
        "restriction-triage",
        "io/os/package=nil 제약을 관찰했고 INFO server에서 Redis 버전이 7.x로 확인됐다. "
        "동일 EVAL/조회를 반복하지 말고, 이 버전에 적용되는 Lua 샌드박스 공개 권고문과 "
        "완화책을 검색하라. 검색 결과를 플래그 증거로 취급하지 마라.",
        "web_search",
        {"query": "Redis 7.x Lua sandbox io os restricted CVE advisory affected versions mitigation"},
        "search_version_aware_after_restriction",
    )

    # 4) GENERAL non-redis analogue: benign HTTP health passes -> escalate to the
    #    product/version, then research that version's advisories.
    add(
        "http-version-analogue",
        "허가된 웹 대상에서 benign `GET /health`가 200을 반환했고 응답 `Server` 헤더에 "
        "`Apache Tomcat 9.0.80`이 확인됐다. 같은 헬스체크를 반복하지 말고, 확인된 "
        "제품·버전에 적용되는 공개 보안 권고문과 영향 범위를 검색하라. 검색은 대상의 "
        "실제 응답을 대신하지 않는다.",
        "web_search",
        {"query": "Apache Tomcat 9.0.80 security advisory affected versions"},
        "search_after_observed_version_general",
    )

    # 5) GENERAL non-redis analogue: SSH banner version -> research advisories.
    add(
        "ssh-version-analogue",
        "허가된 시험 서비스의 SSH 배너로 `SSH-2.0-OpenSSH_8.9p1`을 수신했다. 배너를 "
        "다시 읽지 말고, 광고된 버전을 식별해 해당 버전의 공개 보안 권고문과 완화 지침을 "
        "검색하라. 이후 판단은 대상의 실제 응답으로 검증한다.",
        "web_search",
        {"query": "OpenSSH 8.9p1 security advisory affected versions"},
        "search_after_observed_version_general",
    )

    all_rows = rows + added
    assert all(r["source"] == SOURCE for r in all_rows)
    assert all(r["kind"] == "action" for r in added)
    assert all(r["completion"].startswith("<tool_call>\n") for r in added)
    assert len(all_rows) == 325

    OUT.mkdir(exist_ok=True)
    (OUT / "train.jsonl").write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in all_rows),
        encoding="utf-8",
    )
    counts = Counter(r["kind"] for r in all_rows)
    manifest = {
        "version": 1,
        "source": SOURCE,
        "counts": {"train": len(all_rows), **dict(counts)},
        "composition": {"v60_base": len(rows), "v61_escalation": len(added)},
        "training_intent": (
            "epochs<=2; after a benign EVAL confirms the Lua path, escalate ONCE to a "
            "read-only capability probe (io/os/package/loadstring/cjson presence) instead "
            "of repeating the benign call; on an observed restriction, research the "
            "version's public advisory and confirm the target version; general HTTP/SSH "
            "version analogues; target raw response alone establishes a finding; "
            "evaluate on untouched holdout"
        ),
    }
    (OUT / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print(len(all_rows), len(added), dict(counts))


if __name__ == "__main__":
    main()
