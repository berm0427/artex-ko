package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "사용자가 작업을 일시정지함",
		"사용자가 작업 제어 API(POST /api/tasks/{id}/control, action=pause)로 작업을 일시정지했습니다. 이번 Planner/Worker 실행은 취소되며, 실행 중이던 의도는 frontier(open)로 돌아가 재개 후 처음부터 다시 실행됩니다.")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "오케스트레이션 Agent가 작업을 일시정지함",
		"오케스트레이션 Agent가 pause_task 도구로 작업을 일시정지했습니다. 이번 Planner/Worker 실행은 취소되며, 실행 중이던 의도는 재개 후 다시 실행됩니다.")
	AbortTaskDeleted = cause("task_deleted", "작업이 삭제됨",
		"작업 삭제(DELETE /api/tasks/{id})가 진행되어 실행 중인 Planner, Worker 및 메인 Agent가 취소되었습니다. 이번 실행 결과는 더 이상 사용되지 않습니다.")
	AbortPausedOnReload = cause("paused_on_reload", "백엔드가 작업의 일시정지 상태를 복원함",
		"백엔드 시작 시 데이터베이스에 저장된 작업 일시정지 상태를 복원했습니다. 이번 실행은 취소되었습니다.")
	AbortGoalMet = cause("goal_met", "플래너가 작업 목표 달성을 판정함",
		"플래너가 작업 목표 달성을 판정하고 상태를 done으로 변경한 뒤 실행 중인 Worker를 취소했습니다. 해당 의도는 실패가 아니라 stopped로 표시됩니다.")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "작업 시간 초과 마무리 대기 시간이 끝남",
		"작업 timeout 후 실행 중인 Worker의 정상 마무리를 기다렸지만 90초 유예 시간도 부족해 강제 취소했습니다. 의도는 exhausted로 표시되고 이미 기록한 사실과 자산은 유지됩니다.")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "플래너가 이 의도를 종료함",
		"플래너가 kill_work 도구로 이 의도를 종료했습니다. 의도는 stopped로 표시되며 자동으로 다시 선택되지 않습니다.")
	AbortWorkPausedByUser = cause("work_paused_by_user", "사용자가 이 Worker 의도를 일시정지함",
		"사용자가 실행 중인 Worker를 일시정지했습니다. 호출은 취소되고 의도는 paused로 전환됩니다. 이미 기록한 의도, 사실, 취약점 및 활동 기록은 유지되며 재개 후 처음부터 다시 실행됩니다.")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "사용자가 이 Worker 의도를 삭제함",
		"사용자가 실행 중인 Worker를 삭제했습니다. 호출은 취소되며, 서버는 사용자가 선택한 삭제 방식에 따라 의도와 관련 하위 노드를 처리합니다.")
	AbortWorkFinished = cause("work_finished", "Worker가 정상 종료되어 context를 해제함",
		"Worker가 정상 종료되어 엔진이 context 자원을 해제했습니다. 실행 중단이 아닙니다.")
	AbortPausedRaceGuard = cause("paused_race_guard", "작업 일시정지 중 새 실행 시작을 거부함",
		"작업이 일시정지된 동안 엔진이 새 실행 context 생성을 거부했습니다. 이미 선택된 의도는 frontier로 돌아갑니다.")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "사용자가 이번 대화를 중지함",
		"사용자가 중지 버튼을 눌러 이번 메인 Agent 또는 대화 Agent 실행을 종료했습니다. 기존 활동 기록은 유지됩니다.")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "작업 일시정지로 메인 Agent 대화가 중단됨",
		"사용자가 작업을 일시정지해 실행 중이던 메인 Agent 대화도 취소되었습니다. 기존 활동 기록은 유지됩니다.")
	AbortChatTurnFinished = cause("chat_turn_finished", "이번 대화가 정상 종료되어 context를 해제함",
		"이번 대화가 정상 종료되어 서버가 context 자원을 해제했습니다. 실행 중단이 아닙니다.")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "백엔드 프로세스가 종료 중임",
		"백엔드가 SIGINT 또는 SIGTERM을 받아 재시작, 업데이트 또는 종료 중입니다. 실행 중인 Agent는 취소되며, 재시작 후 남은 running 의도는 open으로 되돌아가 다시 실행됩니다.")
	AbortRunHardTimeout = cause("run_hard_timeout", "단일 실행의 강제 시간 초과가 발생함",
		"단일 실행이 시간 예산과 추가 유예 시간을 넘겼습니다. 중단 직전 반환되지 않은 모델 요청이나 도구 호출을 확인하세요.")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "상위 context가 deadline에 도달함",
			"상위 context가 deadline에 도달했지만 WithTimeoutCause에 명시적 원인이 없습니다: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "취소 원인이 지정되지 않음",
			"상위 context가 취소됐지만 context.WithCancelCause에 명시적 원인이 없습니다. agent/cancelcause.go에서 원인을 등록하세요.", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
