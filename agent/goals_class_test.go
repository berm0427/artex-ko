package agent

import (
	"strings"
	"testing"
)

func TestNormalizeDecomposedGoal(t *testing.T) {
	tests := []struct {
		name, source string
		in, want     goalItem
	}{
		{
			name:   "inferred IDOR class is removed",
			source: "파일 다운로드 기능의 정상 요청과 비정상 요청을 비교한다",
			in:     goalItem{Text: "[IDOR] 실제 HTTP 증거를 보고", VulnClass: "IDOR"},
			want:   goalItem{Text: "실제 HTTP 증거를 보고"},
		},
		{
			name:   "inferred bracket class without field is removed",
			source: "파일 다운로드 기능을 검증한다",
			in:     goalItem{Text: "[Path Traversal] 실제 HTTP 증거를 보고"},
			want:   goalItem{Text: "실제 HTTP 증거를 보고"},
		},
		{
			name:   "explicit class is retained",
			source: "IDOR 검증을 수행한다",
			in:     goalItem{Text: "[IDOR] 실제 HTTP 증거를 보고", VulnClass: "IDOR"},
			want:   goalItem{Text: "[IDOR] 실제 HTTP 증거를 보고", VulnClass: "IDOR"},
		},
		{
			name:   "substring is not an explicit class",
			source: "validordinary 문자열 확인",
			in:     goalItem{Text: "증거를 보고", VulnClass: "IDOR"},
			want:   goalItem{Text: "증거를 보고"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDecomposedGoal(tt.in, tt.source); got != tt.want {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestGoalsSystemRequiresExplicitClass(t *testing.T) {
	sys := goalsSystem(t.TempDir(), false)
	if !strings.Contains(sys, "vulnclass는 사용자가 해당 분류를 명시한 경우에만 채우세요") ||
		!strings.Contains(sys, "목표 문장에 추정 분류를 붙이지 마세요") {
		t.Fatal("goals system prompt is missing the code-owned explicit-class rule")
	}
}
