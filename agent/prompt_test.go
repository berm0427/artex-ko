package agent

import (
	"runtime"
	"strings"
	"testing"
)

func TestRenderSystemOverrideAndFallback(t *testing.T) {
	t.Cleanup(func() { PromptOverride = nil })

	// no override → built-in default
	PromptOverride = nil
	if got := renderSystem("planner", "DEFAULT", PlannerVars{Goal: "g"}); got != "DEFAULT" {
		t.Fatalf("no override should give default, got %q", got)
	}

	// override → rendered with vars
	PromptOverride = func(k string) (string, bool) {
		if k == "planner" {
			return "目标:{{.Goal}} 范围:{{.Scope}}", true
		}
		return "", false
	}
	if got := renderSystem("planner", "DEFAULT", PlannerVars{Goal: "拿下X", Scope: "*.x.com"}); got != "目标:拿下X 范围:*.x.com" {
		t.Fatalf("override render: %q", got)
	}

	// override referencing a non-catalog var → execution error → fallback to default
	PromptOverride = func(k string) (string, bool) { return "{{.NotInCatalog}}", true }
	if got := renderSystem("planner", "DEFAULT", PlannerVars{Goal: "x"}); got != "DEFAULT" {
		t.Fatalf("bad var should fall back to default, got %q", got)
	}

	// full plannerSystem path: DB body [A] is honored, then the code-owned tail
	// [C] (中间产物输出规约) is ALWAYS appended — editing the body can't drop it.
	PromptOverride = func(k string) (string, bool) { return "PLANNER {{.Goal}}", true }
	got := plannerSystem("拿下X", "/data", "/data")
	if !strings.HasPrefix(got, "PLANNER 拿下X") {
		t.Fatalf("plannerSystem body not honored: %q", got)
	}
	if !strings.Contains(got, "중간 산출물 경로") || !strings.Contains(got, "/data") {
		t.Fatalf("plannerSystem missing code-owned artifact tail: %q", got)
	}

	// worker dual-text via {{if .ProxyAddr}} in a user template, plus the code tail:
	// [B] trafficTool present only when RECORDING (caCert set — the MITM is on, so
	// the traffic_* tools exist), [C] artifact spec always present. The trafficTool
	// block is gated on the CA (arg 2), NOT on ProxyAddr — a global egress proxy
	// with capture off routes traffic but records nothing.
	PromptOverride = func(k string) (string, bool) {
		return "{{if .ProxyAddr}}走代理 {{.ProxyAddr}}{{else}}手动{{end}}", true
	}
	recording := workerSystem("127.0.0.1:8080", "/ca.pem", "/data", "/data")
	if !strings.HasPrefix(recording, "走代理 127.0.0.1:8080") {
		t.Fatalf("worker proxy branch body: %q", recording)
	}
	if !strings.Contains(recording, "traffic_search") {
		t.Fatalf("worker while recording should inject trafficTool: %q", recording)
	}
	if strings.Contains(recording, "traffic_refs") {
		t.Fatalf("worker bypassed shared optional evidence policy: %q", recording)
	}
	if !strings.Contains(recording, "중간 산출물 경로") {
		t.Fatalf("worker missing artifact tail: %q", recording)
	}
	if !strings.Contains(recording, "로컬 netstat의 LISTEN 목록") || !strings.Contains(mainAgentSystem("g", "/data", "/data"), "로컬 netstat의 LISTEN 목록") {
		t.Fatal("worker and main agent must not infer remote listener state from local netstat")
	}
	// Egress proxy set but capture OFF (no CA): the ProxyAddr template branch still
	// renders, but the trafficTool block must NOT — those tools are not registered.
	egressOnly := workerSystem("127.0.0.1:8080", "", "/data", "/data")
	if !strings.HasPrefix(egressOnly, "走代理 127.0.0.1:8080") {
		t.Fatalf("worker egress-only branch body: %q", egressOnly)
	}
	if strings.Contains(egressOnly, "traffic_search") {
		t.Fatalf("worker without recording must NOT inject trafficTool: %q", egressOnly)
	}
	noProxy := workerSystem("", "", "/data", "/data")
	if !strings.HasPrefix(noProxy, "手动") {
		t.Fatalf("worker no-proxy branch body: %q", noProxy)
	}
	if strings.Contains(noProxy, "traffic_search") {
		t.Fatalf("worker without proxy must NOT inject trafficTool: %q", noProxy)
	}
}

func TestEvidenceLedMethodRuleSurvivesPromptOverrides(t *testing.T) {
	t.Cleanup(func() { PromptOverride = nil })
	PromptOverride = func(string) (string, bool) { return "editable body", true }
	for role, prompt := range map[string]string{
		"planner": plannerSystem("goal", "/data", "/data"),
		"worker":  workerSystem("", "", "/data", "/data"),
	} {
		for _, required := range []string{"실제로 제공된 표면", "잠정 가설", "관찰과 맞지 않으면 분류와 도구를 바꾸세요", "특정 유형 하나만 강제하지 않으며"} {
			if !strings.Contains(prompt, required) {
				t.Errorf("%s prompt lacks evidence-led method rule %q", role, required)
			}
		}
	}
}

// TestLangDirectiveAppendedToUserFacingRoles pins the artex-ko localization tail:
// every user-facing role's system prompt must end with the code-owned Korean
// output-language directive, and a DB-edited body must NOT be able to drop it.
func TestLangDirectiveAppendedToUserFacingRoles(t *testing.T) {
	t.Cleanup(func() { PromptOverride = nil })

	// The directive forces Korean OUTPUT and preserves raw technical strings; both
	// signals must be present. 한국어 marker + verbatim-preservation clause.
	dir := langDirective()
	if !strings.Contains(dir, "한국어") {
		t.Fatalf("langDirective must force Korean output, got %q", dir)
	}
	if !strings.Contains(dir, "payload") || !strings.Contains(dir, "그대로 보존") {
		t.Fatalf("langDirective must keep commands/payloads verbatim, got %q", dir)
	}
	// L1 anti-drift hardening: the directive must (1) forbid leaking the Chinese
	// instruction/brain language into user-facing text (planner situation-summary
	// drift), and (2) forbid mirroring the target/material language — e.g. an
	// English target app — in the display fields (report_finding drift). Both
	// clauses are locked here so a future edit can't silently drop them.
	if !strings.Contains(dir, "기술 원문을 제외한 외국어 문장을 사용자에게 출력하지 마세요") {
		t.Fatalf("langDirective must forbid leaking Chinese to the user, got %q", dir)
	}
	if !strings.Contains(dir, "대상 자료나 기존 지시가 다른 언어로 작성되어 있어도") {
		t.Fatalf("langDirective must forbid mirroring the target/material language, got %q", dir)
	}
	if !strings.Contains(dir, "플래너의 상황 요약") {
		t.Fatalf("langDirective must name the planner situation summary as user-facing, got %q", dir)
	}
	if runtime.GOOS == "windows" && (!strings.Contains(dir, "PowerShell 5.1") || !strings.Contains(dir, "완전히 같은 실패 명령은 다시 호출하지 마세요")) {
		t.Fatalf("Windows directive must describe shell syntax and stop identical retries, got %q", dir)
	}

	// Even with a DB body that is pure non-directive text, the code-owned tail is
	// still appended for each user-facing builder — identical guarantee to the
	// artifact tail. A custom body can never translate away the Korean mandate.
	PromptOverride = func(string) (string, bool) { return "BODY-ONLY", true }
	cases := map[string]string{
		"worker":    workerSystem("", "", "/data", "/data"),
		"planner":   plannerSystem("g", "/data", "/data"),
		"mainagent": mainAgentSystem("g", "/data", "/data"),
		"chat":      chatSystem("chat", "/data", "/data"),
		// goals is user-facing too: set_goals/set_constraints persist goal and
		// constraint nodes shown in the UI graph/plan tab. withScope=true exercises
		// the longer assembly (body + scope tail), so the Korean tail must still land
		// last — after both the body and the code-owned scope tail.
		"goals": goalsSystem("/data", true),
	}
	for role, sys := range cases {
		if !strings.HasPrefix(sys, "BODY-ONLY") {
			t.Fatalf("%s: DB body not honored: %q", role, sys)
		}
		if !strings.Contains(sys, "한국어") {
			t.Fatalf("%s: missing Korean output-language tail: %q", role, sys)
		}
		// The directive is the tail — it must come AFTER the body (recency).
		if strings.Index(sys, "한국어") <= strings.Index(sys, "BODY-ONLY") {
			t.Fatalf("%s: langDirective must be appended after the body: %q", role, sys)
		}
	}
}
