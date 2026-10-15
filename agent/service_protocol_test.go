package agent

import (
	"strings"
	"testing"
)

func TestServiceProtocolRuleSurvivesPromptOverrides(t *testing.T) {
	old := PromptOverride
	defer func() { PromptOverride = old }()
	PromptOverride = func(string) (string, bool) { return "custom prompt", true }
	for role, prompt := range map[string]string{
		"planner": plannerSystem("test", "/data", "/work"),
		"worker":  workerSystem("", "", "/data", "/work"),
	} {
		for _, required := range []string{"HTTP 상태줄", "HTTP/0.9 오류", "raw TCP", "기존 사실이나 의도", "반복 탐색 방지", "PowerShell 5.1"} {
			if !strings.Contains(prompt, required) {
				t.Errorf("%s prompt lacks protocol safeguard %q", role, required)
			}
		}
	}
}

func TestWorkerDoesNotFinishWithoutAnEvidenceAttempt(t *testing.T) {
	prompt := workerSystem("", "", "/data", "/work")
	for _, required := range []string{"도구 호출 없이 종료하지 마세요", "node_detail", "get_worker_trace", "허가된 대상", "서로 한 조건만 다른 입력", "'취약점 없음'의 증거가 아닙니다"} {
		if !strings.Contains(prompt, required) {
			t.Errorf("worker prompt lacks evidence-attempt guidance %q", required)
		}
	}
}

func TestPlannerAvoidsRepeatedKeywordGuessing(t *testing.T) {
	prompt := plannerSystem("test", "/data", "/work")
	for _, required := range []string{"단어만 바꾼 의도", "전송 바이트·수신 바이트", "한 번에 한 변수만", "단순 무응답"} {
		if !strings.Contains(prompt, required) {
			t.Errorf("planner prompt lacks blind-protocol guidance %q", required)
		}
	}
}
