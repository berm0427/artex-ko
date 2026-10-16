package agent

import (
	"strings"
	"testing"
)

func TestHypothesisCoverageRuleSurvivesPromptOverrides(t *testing.T) {
	old := PromptOverride
	defer func() { PromptOverride = old }()
	PromptOverride = func(string) (string, bool) { return "custom prompt", true }

	for role, prompt := range map[string]string{
		"planner": plannerSystem("test", "/data", "/work"),
		"worker":  workerSystem("", "", "/data", "/work"),
	} {
		for _, required := range []string{"핵심 전제조건", "입력 크기·구조·상태·버전·권한·환경", "시험한 범위에서 관찰되지 않음", "핵심 조건을 충족했는데도 재현되지 않음", "다음 로컬 실험"} {
			if !strings.Contains(prompt, required) {
				t.Errorf("%s prompt lacks hypothesis coverage rule %q", role, required)
			}
		}
	}
}
