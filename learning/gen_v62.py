"""Additive v62: when version-confirm is BLOCKED (NOPERM / hidden / unavailable),
do NOT idle or loop version attempts — search with the service name + observed
restriction (version unknown), or report the blocker. Fixes live #786 where the
worker hit INFO -NOPERM, could not get the Redis version, and the planner went
idle instead of calling web_search. Additive on v61 (325 rows)."""
import copy
import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent
BASE = ROOT / "agentic_v61_proto"
OUT = ROOT / "agentic_v62_proto"
SOURCE = "agentic_v62_pivot"


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
    assert len(rows) == 325, len(rows)
    assert all(row["source"] == "agentic_v61_pivot" for row in rows)

    # Reuse the v61 template that already carries tcp_session(resp_args) + web_search.
    template = next(
        row for row in rows if row["sample_id"] == "v61-restriction-triage_v00"
    )
    assert any(
        t["function"]["name"] == "web_search" for t in template["tools"]
    ), "template must expose web_search"
    system = template["messages"][0]["content"]
    tools = copy.deepcopy(template["tools"])

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
            "case_id": f"v62-{slug}",
            "sample_id": f"v62-{slug}_v00",
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

    # 1) THE core fix: version-confirm via INFO is -NOPERM. Do NOT idle or repeat
    #    INFO. Search with the service + the OBSERVED restriction, version unknown.
    add(
        "redis-version-noperm-search",
        "io/os/package=nil 제약을 EVAL로 관찰했다. 버전 확인을 위해 INFO server를 보냈더니 "
        "`-NOPERM this user has no permissions to run the 'info' command`로 거부됐다. "
        "버전을 알 수 없다고 멈추거나 INFO를 반복하지 마라. 확인된 서비스(Redis)와 관찰된 "
        "제약(Lua io/os/package 제거)으로, 버전 불명 상태 그대로 공개 권고문을 검색하라.",
        "web_search",
        {"query": "Redis Lua scripting sandbox restricted globals security advisories mitigation"},
        "search_when_version_confirm_blocked",
    )

    # 2) Redis variant: the EVAL fallback for version is ALSO blocked/empty. Still
    #    search on observed behavior instead of looping version probes.
    add(
        "redis-version-evalfallback-search",
        "INFO가 NOPERM이라 EVAL로 `return redis.call('INFO','server')`를 우회 시도했으나 "
        "역시 빈 응답/권한 제한으로 버전을 못 얻었다. 버전 획득을 더 반복하지 말고, 관찰된 "
        "Lua 샌드박스 제약을 근거로 해당 동작의 공개 취약점/완화책을 검색하라.",
        "web_search",
        {"query": "Redis Lua scripting permissions restricted commands security advisories mitigation"},
        "search_when_version_unobtainable",
    )

    # 3) GENERAL HTTP analogue: product known but version header stripped/absent.
    #    Search by product + observed behavior, don't stall waiting for a version.
    add(
        "http-version-hidden-search",
        "허가된 웹 대상이 `Server: nginx` 헤더만 주고 버전은 숨겨져 있다(버전 표기 없음). "
        "`GET /` 등에서 추가 버전 단서도 없다. 버전을 알아낼 때까지 같은 요청을 반복하지 말고, "
        "확인된 제품(nginx)과 관찰된 동작으로 공개 보안 권고문을 검색하라.",
        "web_search",
        {"query": "nginx reverse proxy known vulnerabilities security advisory configuration"},
        "search_when_version_hidden_general",
    )

    # 4) GENERAL SSH analogue: banner/version suppressed. Search on observed
    #    behavior + product, do not idle.
    add(
        "ssh-banner-suppressed-search",
        "허가된 시험 서비스의 SSH가 배너를 억제해 버전 문자열이 없다(예: `SSH-2.0-` 뒤 제품/버전 비공개). "
        "배너를 다시 읽어도 버전이 안 나온다. 버전 없이도, 관찰된 인증 동작과 프로토콜로 "
        "해당 서비스 부류의 공개 보안 지침을 검색하라.",
        "web_search",
        {"query": "SSH server authentication security advisories hardening"},
        "search_when_banner_suppressed_general",
    )

    # 5) DECISION row (kind=decision, prose): generalize the rule so it is not tied
    #    to one service — a blocked version check must route to search-or-report,
    #    never to idling or repeating the blocked probe.
    added.append({
        "case_id": "v62-blocked-version-decision",
        "sample_id": "v62-blocked-version-decision_v00",
        "source": SOURCE,
        "orig_source": {"synthetic": True, "method": "decision_blocked_version_route"},
        "kind": "decision",
        "messages": [
            {"role": "system", "content": system},
            {"role": "user", "content": (
                "상황: 다음 행동을 정하기 전에, 버전 확인이 권한(NOPERM)·숨김·미응답으로 "
                "막혔을 때의 원칙을 한 문장으로 정하라."
            )},
        ],
        "completion": (
            "버전 확인이 막히면(NOPERM·배너 숨김·빈 응답) 같은 버전 확인을 반복하거나 유휴 상태로 "
            "멈추지 말고, 확인된 서비스명과 관찰된 제약·동작을 근거로 '버전 불명' 상태에서 공개 "
            "권고문을 검색하거나, 검색이 불가하면 막힌 지점을 명시해 보고로 전진한다."
        ),
        "tools": copy.deepcopy(tools),
    })

    all_rows = rows + added
    assert all(r["source"] == SOURCE for r in all_rows)
    assert all(r["completion"] for r in added)
    action_added = [r for r in added if r["kind"] == "action"]
    assert all(r["completion"].startswith("<tool_call>\n") for r in action_added)
    assert len(all_rows) == 330, len(all_rows)

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
        "composition": {"v61_base": len(rows), "v62_blocked_version_search": len(added)},
        "training_intent": (
            "epochs<=2; when version-confirm is BLOCKED (NOPERM / hidden banner / empty "
            "response) do NOT idle or repeat the version probe — search with the service "
            "name + observed restriction at 'unknown version', or report the blocker and "
            "advance; general HTTP(nginx)/SSH(OpenSSH) analogues + one service-agnostic "
            "decision row; evaluate on untouched holdout"
        ),
    }
    (OUT / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print(len(all_rows), len(added), dict(counts))


if __name__ == "__main__":
    main()
