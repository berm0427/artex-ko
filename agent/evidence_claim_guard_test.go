package agent

import (
	"encoding/hex"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func claimGuardToolResult(tool, summary string) db.Activity {
	return db.Activity{Kind: "tool_result", Tool: tool, IsError: false, Summary: summary}
}

func TestFramingErrorCitedAsLuaRestriction(t *testing.T) {
	const framing = "-ERR Protocol error: unbalanced quotes in request\r\n"
	const integer = "-ERR value is not an integer or out of range\r\n"
	const runtime = "-ERR Error running script: attempt to index a nil value (global 'io')\r\n"
	tcp := func(raw string) db.Activity {
		return db.Activity{
			Kind: "tool_result", Tool: "tcp_session",
			Detail: `{"received_hex":"` + hex.EncodeToString([]byte(raw)) + `"}`,
		}
	}
	for _, tc := range []struct {
		name string
		fact factItem
		raw  string
		want bool
	}{
		{"framing via rawget", factItem{Summary: "Lua 샌드박스가 rawget으로 io 접근 불가", EvidenceQuote: framing}, framing, true},
		{"integer framing via rawget", factItem{Summary: "Lua sandbox blocks os via rawget", EvidenceQuote: integer}, integer, true},
		{"empty quote bypass closed", factItem{Summary: "Lua 샌드박스가 rawget으로 io 접근 불가"}, framing, true},
		{"genuine lua runtime never blocked", factItem{Summary: "Lua 샌드박스가 io 차단", EvidenceQuote: runtime}, runtime, false},
		{"no sandbox claim", factItem{Summary: "Redis RESP framing 실패", EvidenceQuote: framing}, framing, false},
		{"unqualified io not matched", factItem{Summary: "Lua sandbox blocks io", EvidenceQuote: framing}, framing, false},
		{"quote mismatch", factItem{Summary: "Lua 샌드박스가 rawget으로 io 접근 불가", EvidenceQuote: "some other text"}, framing, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := framingErrorCitedAsLuaRestriction(tc.fact, []db.Activity{tcp(tc.raw)}); got != tc.want {
				t.Fatalf("framingErrorCitedAsLuaRestriction() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectUngroundedObservedClaim(t *testing.T) {
	const runtimeClaim = "로그인 폼이 /api/echo로 사용자 입력을 반환함"

	static := []db.Activity{claimGuardToolResult("read", "app.py 소스 읽기 완료")}
	succeeded := []db.Activity{static[0], claimGuardToolResult("bash", "HTTP/1.1 200 OK")}
	failed := []db.Activity{claimGuardToolResult("bash", "error: connection refused")}
	tcpDenied := []db.Activity{{Kind: "tool_result", Tool: "tcp_exchange", Detail: `{"received_bytes":9,"received_hex":"2d4e4f5045524d0d0a"}`}}
	tcpEmpty := []db.Activity{{Kind: "tool_result", Tool: "tcp_session", Detail: `{"received_bytes":0,"received_hex":""}`}}

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
			name:       "TCP denial is still a direct observation",
			confidence: "observed",
			summary:    "INFO 요청은 -NOPERM 응답을 반환함",
			activities: tcpDenied,
		},
		{
			name:           "empty TCP result is not an observation",
			confidence:     "observed",
			summary:        runtimeClaim,
			activities:     tcpEmpty,
			wantUngrounded: true,
		},
		{
			name:           "errored TCP tool result is not an observation",
			confidence:     "observed",
			summary:        runtimeClaim,
			activities:     []db.Activity{{Kind: "tool_result", Tool: "tcp_exchange", IsError: true, Detail: tcpDenied[0].Detail}},
			wantUngrounded: true,
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
