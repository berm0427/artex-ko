package server

import "testing"

func TestValidateKoreanReportCause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report string
		want   bool
	}{
		{"unsupported certainty", "## 원인 분석\n사용자 식별 인자를 잘못 처리하여 권한이 부여되었습니다.\n## 수정 권고\n권한 검사", false},
		{"explicit uncertainty", "## 원인 분석\n응답으로 권한 검사 실패가 의심되지만 구현상 원인은 미확인입니다.\n## 수정 권고\n권한 검사", true},
		{"no cause section", "## 증거\n실측 응답", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateKoreanReportCause(tc.report) == nil; got != tc.want {
				t.Fatalf("accepted=%v, want %v", got, tc.want)
			}
		})
	}
}
