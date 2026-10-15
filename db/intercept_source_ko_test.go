package db

import "testing"

func TestInterceptSourceAcceptsKoreanAndLegacyModelPrefixes(t *testing.T) {
	for _, reason := range []string{"[모델] 사람 승인 요청", "[模型] 请确认"} {
		if got := interceptSource(0, reason); got != "model" {
			t.Fatalf("reason %q: source = %q", reason, got)
		}
	}
}
