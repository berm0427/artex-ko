package agent

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
)

const ungroundedObservedClaimReason = "이 사실은 구체적 런타임/네트워크 관측을 observed로 주장하지만, 이 의도에 성공한 네트워크/실행 활동(bash/shell/redis-cli/curl 등)이 없습니다."

// runtimeObservationSignals: 대상의 런타임/네트워크 동작을 직접 봤다는 주장의 신호(소문자).
var runtimeObservationSignals = []string{
	"/api/", "엔드포인트", "endpoint", "라우트", "route",
	"응답", "respond", "responded", "returns", "반환", "회신",
	"상태 코드", "status code", "http 200", "http 500", "200 ok", "302", "401", "403", "500",
	"접속 결과", "요청 결과", "실행 결과", "실행하니", "실행했더니", "stdout", "stderr", "출력:",
	"셸 획득", "쿼리 결과", "로그인 성공", "접근 성공",
}

// claimClauseSeparators: 주장을 절 단위로 나눈다. 경로·파일명의 점은 구분자가 아니다.
var claimClauseSeparators = strings.NewReplacer(
	". ", "\n",
	"。", "\n",
	",", "\n",
	";", "\n",
)

// negativeObservationPhrases: 부정 관찰 문구를 지운다.
var negativeObservationPhrases = strings.NewReplacer(
	"응답 없", " ",
	"응답이 없", " ",
	"응답하지 않", " ",
	"반환하지 않", " ",
	"no response", " ",
	"not respond", " ",
	"n't respond", " ",
)

// negatedClauseTargets: 부정 관찰 절에서 관측 대상만 가리키는 신호를 지운다.
// 200 OK·회신처럼 긍정 결과를 뜻하는 신호는 남겨 그대로 검사한다.
var negatedClauseTargets = strings.NewReplacer(
	"/api/", " ",
	"엔드포인트", " ",
	"endpoint", " ",
	"라우트", " ",
	"route", " ",
	"접속 결과", " ",
	"요청 결과", " ",
	"실행 결과", " ",
	"쿼리 결과", " ",
)

// hasRuntimeObservationClaim: 절마다 부정 관찰을 걷어낸 뒤 긍정 관측 신호가 남는지 검사한다.
func hasRuntimeObservationClaim(claim string) bool {
	for _, clause := range strings.Split(claimClauseSeparators.Replace(claim), "\n") {
		asserted := negativeObservationPhrases.Replace(clause)
		if asserted != clause {
			asserted = negatedClauseTargets.Replace(asserted)
		}
		if containsAny(asserted, runtimeObservationSignals...) {
			return true
		}
	}
	return false
}

// detectUngroundedObservedClaim: observed/confirmed 사실이 성공한 네트워크/실행 활동 없이
// 런타임 관측을 주장하면 ungrounded=true. reason은 관측값을 노출하지 않는 고정 문구다.
func detectUngroundedObservedClaim(confidence, summary, detail string, activities []db.Activity) (reason string, ungrounded bool) {
	level := strings.ToLower(strings.TrimSpace(confidence))
	if level != "observed" && level != "confirmed" {
		return "", false
	}
	if !hasRuntimeObservationClaim(strings.ToLower(summary + "\n" + detail)) {
		return "", false
	}
	if hasSuccessfulExecOrNetworkActivity(activities) || hasObservedTCPResponse(activities) {
		return "", false
	}
	return ungroundedObservedClaimReason, true
}

// A Redis -NOPERM reply is still an observed network response. The active
// finding guard deliberately keeps its stricter success rule; this check only
// lets record_fact store an exact negative observation.
func hasObservedTCPResponse(activities []db.Activity) bool {
	for _, activity := range activities {
		if activity.Kind != "tool_result" || activity.IsError {
			continue
		}
		tool := strings.ToLower(activity.Tool)
		if tool != "tcp_exchange" && tool != "tcp_session" {
			continue
		}
		var response struct {
			ReceivedBytes int    `json:"received_bytes"`
			ReceivedHex   string `json:"received_hex"`
		}
		if json.Unmarshal([]byte(activity.Detail), &response) != nil || response.ReceivedBytes <= 0 {
			continue
		}
		decoded, err := hex.DecodeString(response.ReceivedHex)
		if err == nil && len(decoded) == response.ReceivedBytes {
			return true
		}
	}
	return false
}

// checkFactGrounding: 사실의 의도 활동을 불러와 근거 없는 관측 주장을 거부한다.
// report_finding과 같이 활동 조회 실패 시에는 기록을 막지 않는다.
func (t *ToolSet) checkFactGrounding(it factItem, defaultIntent int64) error {
	intentID := pid(it.IntentID)
	if intentID <= 0 {
		intentID = defaultIntent
	}
	if intentID <= 0 {
		return nil
	}
	acts, _, err := t.ts.ActivityPage(db.ActivitySessionFilter{NodeID: &intentID}, 0, 600)
	if err != nil {
		return nil
	}
	for i := range acts {
		if acts[i].Kind != "tool_result" || acts[i].IsError {
			continue
		}
		tool := strings.ToLower(acts[i].Tool)
		if tool != "tcp_exchange" && tool != "tcp_session" {
			continue
		}
		if detail, detailErr := t.ts.ActivityDetail(acts[i].ID); detailErr == nil {
			acts[i].Detail = detail
		}
	}
	if framingErrorCitedAsLuaRestriction(it, acts) {
		return fmt.Errorf("인용한 응답은 RESP 프레이밍 오류(-ERR Protocol error / not an integer)입니다. Lua 샌드박스 제한의 근거로 기록하지 말고 resp_args로 다시 요청해 실제 실행 응답을 확인하세요")
	}
	if reason, ungrounded := detectUngroundedObservedClaim(it.Confidence, it.Summary, it.Detail, acts); ungrounded {
		return fmt.Errorf("%s 실제로 대상에 요청·실행해 응답을 확인한 뒤 observed로 기록하거나, 미관측 추론이면 confidence를 inferred로 낮추고 다음 검증 동작을 제시하세요", reason)
	}
	return nil
}

// framingErrorCitedAsLuaRestriction rejects a fact that claims a Lua sandbox
// restriction (io/os/package removed) when its evidence is really a Redis RESP
// framing/argument error — i.e. the inline EVAL never reached Lua at all, so the
// right move is to resend via resp_args, not to record a sandbox conclusion.
// It deliberately NEVER fires when the intent produced a genuine Lua runtime
// error ("nil value" / "error running script"), so a real sandbox finding is
// never blocked. io/os/package are matched only in qualified forms (rawget,
// quoted globals, Korean 차단/제거) to avoid matching "version"/"position"/etc.
func framingErrorCitedAsLuaRestriction(it factItem, activities []db.Activity) bool {
	claim := strings.ToLower(it.Summary + "\n" + it.Detail)
	if !containsAny(claim, "lua", "sandbox", "샌드박스") ||
		!containsAny(claim, "nil value", "rawget", "'io'", "'os'", "'package'",
			"io 제거", "os 제거", "package 제거", "io 차단", "os 차단", "package 차단",
			"io 접근", "os 접근", "package 접근") {
		return false
	}
	quote := strings.ToLower(strings.TrimSpace(it.EvidenceQuote))
	sawFraming := false
	for _, activity := range activities {
		if activity.Kind != "tool_result" || activity.IsError {
			continue
		}
		tool := strings.ToLower(activity.Tool)
		if tool != "tcp_exchange" && tool != "tcp_session" {
			continue
		}
		var response struct {
			ReceivedHex string `json:"received_hex"`
		}
		if json.Unmarshal([]byte(activity.Detail), &response) != nil {
			continue
		}
		raw, err := hex.DecodeString(response.ReceivedHex)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(raw))
		// A genuine Lua runtime error means the sandbox claim may be legitimate:
		// never block it, regardless of any framing errors elsewhere.
		if strings.Contains(lower, "nil value") || strings.Contains(lower, "error running script") {
			return false
		}
		if !strings.Contains(lower, "-err protocol error") && !strings.Contains(lower, "-err value is not an integer") {
			continue
		}
		// Empty quote → the fact cited no specific bytes; the qualified claim plus
		// a real framing error in this intent is enough. Otherwise require the
		// quote to actually occur in this framing response.
		if quote == "" || strings.Contains(lower, quote) {
			sawFraming = true
		}
	}
	return sawFraming
}
