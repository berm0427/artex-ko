package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/harness"
)

// runTrace retains the latest tool call so an interrupted run can identify the
// operation that was still in flight.
type runTrace struct {
	startedAt time.Time
	id        string
	name      string
	input     string
	at        time.Time
	pending   bool
}

func (t *runTrace) start(id, name, input string) {
	t.id, t.name, t.input, t.at, t.pending = id, name, input, time.Now(), true
}

func (t *runTrace) done(id string) {
	if id == t.id {
		t.pending = false
	}
}

var reasonHint = map[harness.TerminalReason]string{
	harness.ReasonCompleted:         "모델이 정상 종료됐지만 문자 요약을 남기지 않았습니다. 사실과 자산은 이번 도구 호출 기록을 기준으로 확인하세요.",
	harness.ReasonMaxTurns:          "단계 상한(MaxTurns)에 도달했습니다. SDK가 마무리와 결과 기록을 수행했으며 의도는 exhausted로 표시됩니다.",
	harness.ReasonTimeout:           "단일 실행 시간 예산(MaxDuration)에 도달했습니다. 실행 중인 도구를 중단하고 확인된 사실과 자산을 기록합니다.",
	harness.ReasonModelError:        "모델 또는 API 호출이 실패했습니다. 네트워크, 인증, 제한 또는 공급자 오류를 확인하세요.",
	harness.ReasonBlockingLimit:     "컨텍스트 길이가 상한에 도달해 요청 전송이 차단됐습니다. 의도 범위를 줄이거나 도구 출력을 압축하세요.",
	harness.ReasonPromptTooLong:     "프롬프트가 너무 길고 컨텍스트 압축 재시도도 소진되어 계속할 수 없습니다.",
	harness.ReasonImageError:        "현재 모델이 이번 멀티모달 콘텐츠를 지원하지 않습니다. 비전 모델로 바꾸거나 이미지 출력을 피하세요.",
	harness.ReasonStopHookPrevented: "Stop 훅이 이번 실행의 종료를 막은 뒤 계속하지 못했습니다. 작업 Guard 규칙을 확인하세요.",
	harness.ReasonHookStopped:       "도구 또는 훅이 실행을 중지했습니다. 마지막 tool_result의 차단 설명을 확인하세요.",
	harness.ReasonAbortedStreaming:  "모델 출력 스트리밍 중 실행이 취소됐습니다.",
	harness.ReasonAbortedTools:      "도구 실행 중 실행이 취소됐습니다.",
}

// terminalText renders a terminal event with no final text into a compact summary
// and a Markdown detail block.
func terminalText(ctx context.Context, term *harness.Terminal, tr *runTrace) (string, string) {
	reason := term.Reason
	aborted := reason == harness.ReasonAbortedStreaming || reason == harness.ReasonAbortedTools
	// Prompt may return ctx.Err directly without a terminal event. Preserve the
	// cancellation cause instead of falling back to an empty/unknown terminal reason.
	if reason == "" && ctx.Err() != nil {
		aborted = true
	}

	var sum string
	if aborted {
		_, short, _, ok := AbortReason(ctx)
		if !ok {
			short = "취소 원인을 확인할 수 없음"
		}
		stage := "실행 중"
		switch reason {
		case harness.ReasonAbortedStreaming:
			stage = "모델 출력 단계"
		case harness.ReasonAbortedTools:
			stage = "도구 실행 단계"
		}
		sum = "(실행 중단: " + short + "; " + stage + progressSuffix(term, tr) + "에서 중단되어 미완료)"
	} else if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
		sum = "(실행 예산 상한(" + string(reason) + ")에 도달해 결과를 기록하고 마무리함" + progressSuffix(term, tr) + "; 문자 요약 없음)"
	} else {
		hint := terminalReasonHint(reason)
		sum = "(문자 요약 없음, 최종 상태 " + terminalReasonLabel(reason) + ": " + firstLine(hint, 80) + ")"
	}

	var b strings.Builder
	b.WriteString(sum)
	b.WriteString("\n\n")
	displayReason := terminalReasonLabel(reason)
	fmt.Fprintf(&b, "- **최종 상태**: `%s` - %s\n", displayReason, terminalReasonHint(reason))
	if aborted {
		code, _, why, ok := AbortReason(ctx)
		if ok {
			fmt.Fprintf(&b, "- **중단 원인** (`%s`): %s\n", code, why)
		} else {
			b.WriteString("- **중단 원인**: 확인할 수 없습니다. 취소 측에서 명시적 원인을 제공하지 않았을 수 있습니다.\n")
		}
	}
	if term.Err != nil {
		fmt.Fprintf(&b, "- **하위 오류**: `%v`\n", term.Err)
	}
	if aborted && strings.TrimSpace(term.Text) != "" {
		b.WriteString("- **취소 전에 생성된 일부 출력**:\n\n")
		b.WriteString(term.Text)
		b.WriteString("\n\n")
	}
	if term.Turns > 0 {
		fmt.Fprintf(&b, "- **실행 횟수**: 모델 회차 %d번\n", term.Turns)
	}
	if !tr.startedAt.IsZero() {
		fmt.Fprintf(&b, "- **이번 실행 시간**: %s\n", roundDur(time.Since(tr.startedAt)))
	}
	if u := term.Usage; u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens > 0 {
		fmt.Fprintf(&b, "- **누적 token**: 입력 %d / 출력 %d / 캐시 읽기 %d / 캐시 쓰기 %d\n",
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	if tr.name == "" {
		b.WriteString("- **도구 호출**: 이번 실행은 도구 호출 없이 종료됐습니다.\n")
	} else if tr.pending {
		fmt.Fprintf(&b, "- **중단 시 실행 중이던 도구**: `%s` (%s 동안 실행, **결과 미반환**)\n\n  ```json\n  %s\n  ```\n",
			tr.name, roundDur(time.Since(tr.at)), firstLine(tr.input, 300))
	} else {
		fmt.Fprintf(&b, "- **중단 전 마지막 도구**: `%s` (정상 반환됨)\n", tr.name)
	}
	return sum, b.String()
}

func terminalReasonLabel(reason harness.TerminalReason) string {
	if reason == "" {
		return "context_canceled"
	}
	return string(reason)
}

func terminalReasonHint(reason harness.TerminalReason) string {
	if hint := reasonHint[reason]; hint != "" {
		return hint
	}
	if reason == "" {
		return "실행 context가 취소됐지만 하위 계층에서 Terminal 이벤트를 만들지 않았습니다."
	}
	return "알 수 없는 최종 상태입니다. 새 TerminalReason에 대한 reasonHint를 추가하세요."
}

func progressSuffix(term *harness.Terminal, tr *runTrace) string {
	var parts []string
	if term.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d회", term.Turns))
	}
	if !tr.startedAt.IsZero() {
		parts = append(parts, roundDur(time.Since(tr.startedAt)))
	}
	if len(parts) == 0 {
		return ""
	}
	return ", 실행 " + strings.Join(parts, " / ")
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	case d < time.Hour:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Minute).String()
	}
}
