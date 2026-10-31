package agent

import (
	"testing"

	"github.com/Autumn-27/artex/db"
)

func claimGuardToolResult(tool, summary string) db.Activity {
	return db.Activity{Kind: "tool_result", Tool: tool, IsError: false, Summary: summary}
}

func TestDetectUngroundedObservedClaim(t *testing.T) {
	const runtimeClaim = "로그인 폼이 /api/echo로 사용자 입력을 반환함"

	static := []db.Activity{claimGuardToolResult("read", "app.py 소스 읽기 완료")}
	succeeded := []db.Activity{static[0], claimGuardToolResult("bash", "HTTP/1.1 200 OK")}
	failed := []db.Activity{claimGuardToolResult("bash", "error: connection refused")}

	tests := []struct {
		name           string
		confidence     string
		summary        string
		detail         string
		activities     []db.Activity
		wantUngrounded bool
	}{
		{
			name:           "observed runtime claim + no network activity -> ungrounded",
			confidence:     "observed",
			summary:        runtimeClaim,
			activities:     static,
			wantUngrounded: true,
		},
		{
			name:       "observed runtime claim + successful network activity -> grounded",
			confidence: "observed",
			summary:    runtimeClaim,
			activities: succeeded,
		},
		{
			name:       "inferred confidence -> never ungrounded",
			confidence: "inferred",
			summary:    runtimeClaim,
			activities: static,
		},
		{
			name:       "static source fact (no runtime signal) -> grounded",
			confidence: "observed",
			summary:    "제공된 app.py 소스에 /login 처리 함수가 존재함을 소스에서 확인",
			activities: static,
		},
		{
			name:           "failed network activity still ungrounded",
			confidence:     "observed",
			summary:        runtimeClaim,
			activities:     failed,
			wantUngrounded: true,
		},
		{
			name:           "confirmed confidence is trimmed and case insensitive",
			confidence:     "  CoNfIrMeD  ",
			summary:        runtimeClaim,
			wantUngrounded: true,
		},
		{
			name:           "detail runtime signal is case insensitive",
			confidence:     "observed",
			summary:        "대상 확인",
			detail:         "HTTP 200",
			wantUngrounded: true,
		},
		{
			name:       "empty confidence passes",
			summary:    runtimeClaim,
			activities: static,
		},
		{
			name:       "negative observation passes",
			confidence: "observed",
			summary:    "대상 포트에 연결했으나 응답이 없음",
			activities: failed,
		},
		{
			name:       "negative observation with path and request result passes",
			confidence: "observed",
			summary:    "/api/echo 요청 결과 응답 없음",
			activities: static,
		},
		{
			name:       "negative observation before target passes",
			confidence: "observed",
			summary:    "No response from /api/echo",
			activities: static,
		},
		{
			name:           "negative phrase does not hide other signals",
			confidence:     "observed",
			summary:        "일부 요청은 응답이 없었고 /api/secret은 200 OK를 회신함",
			activities:     static,
			wantUngrounded: true,
		},
		{
			name:           "positive claim before negative phrase in same clause",
			confidence:     "observed",
			summary:        "/api/secret은 200 OK를 회신했으나 /api/echo는 응답이 없음",
			activities:     static,
			wantUngrounded: true,
		},
		{
			name:           "negative clause does not hide separate positive clause",
			confidence:     "observed",
			summary:        "/api/echo 요청 결과 응답 없음, /api/secret 라우트 확인",
			activities:     static,
			wantUngrounded: true,
		},
		{
			name:           "negative summary does not hide positive detail",
			confidence:     "observed",
			summary:        "/api/echo 요청 결과 응답 없음",
			detail:         "/api/secret 라우트 확인",
			activities:     static,
			wantUngrounded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, ungrounded := detectUngroundedObservedClaim(tt.confidence, tt.summary, tt.detail, tt.activities)
			if ungrounded != tt.wantUngrounded {
				t.Fatalf("ungrounded = %v, want %v; reason = %q", ungrounded, tt.wantUngrounded, reason)
			}
			wantReason := ""
			if tt.wantUngrounded {
				wantReason = ungroundedObservedClaimReason
			}
			if reason != wantReason {
				t.Errorf("reason = %q, want %q", reason, wantReason)
			}
		})
	}
}
