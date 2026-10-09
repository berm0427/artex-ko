package agent

import (
	"strings"
	"testing"
)

func TestReporterDefaultPromptKoreanEvidenceFidelity(t *testing.T) {
	for _, required := range []string{
		"한국어 Markdown 보고서",
		"finding_node_id",
		"get_finding_traffic(finding_id)",
		"실제 요청 전문을 확보하지 못했다면 HTTP 요청문을 창작하지 말고",
		"소스 코드나 서버 설정을 확인하지 않았다면",
		"계정 탈취·다른 자원 접근·개인정보 유출·보안 침투",
		"update_finding_report(finding_id=finding_node_id",
	} {
		if !strings.Contains(ReporterDefaultPrompt, required) {
			t.Errorf("reporter prompt missing %q", required)
		}
	}
	for _, forbidden := range []string{"全程中文", "## 概述", "## 受影响范围"} {
		if strings.Contains(ReporterDefaultPrompt, forbidden) {
			t.Errorf("reporter prompt contains old Chinese instruction %q", forbidden)
		}
	}
}
