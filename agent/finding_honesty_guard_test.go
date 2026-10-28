package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestDetectFabricatedActiveFinding(t *testing.T) {
	const exploitSummary = "56바이트 버퍼 오버플로우로 get_shell 실행, cat /home/rao/flag 로 원격 명령 실행 확인"

	tests := []struct {
		name       string
		severity   string
		summary    string
		activities []db.Activity
		want       bool
	}{
		{
			name:     "planner 활동만 존재",
			severity: "critical",
			summary:  exploitSummary,
			activities: []db.Activity{
				{Worker: "planner", Kind: "tool_use", Tool: "set_goals"},
				{Worker: "planner", Kind: "tool_use", Tool: "graph_overview"},
			},
			want: true,
		},
		{
			name:     "docker와 redis 실행 성공",
			severity: "critical",
			summary:  exploitSummary,
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "docker", Summary: "컨테이너 기동 완료"},
				{Kind: "tool_result", Tool: "redis-cli", Summary: "PONG 수신"},
			},
			want: false,
		},
		{
			name:       "정적 분석만 주장",
			severity:   "medium",
			summary:    "Redis Dockerfile·Specfile 정적 원문 확인, 취약 경로 미검증",
			activities: []db.Activity{},
			want:       false,
		},
		{
			name:     "실행 활동이 실패",
			severity: "critical",
			summary:  "원격 명령 실행 확인",
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "bash", Summary: "연결할 수 없습니다(timeout)"},
			},
			want: true,
		},
		{
			name:     "nc 응답 존재",
			severity: "critical",
			summary:  "flag 누출(GoN{ 패턴) 재현",
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "nc", Summary: "응답 수신"},
			},
			want: false,
		},
		{
			name:     "대화형 셸 세션으로 입증",
			severity: "critical",
			summary:  exploitSummary,
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "shell_open", Summary: "세션 연결됨"},
				{Kind: "tool_result", Tool: "shell_send", Summary: "셸 프롬프트 수신"},
			},
			want: false,
		},
		{
			name:     "실제 플래그를 reason에 노출하지 않음",
			severity: "medium",
			summary:  "GoN{private_test_value}",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, got := detectFabricatedActiveFinding(tt.severity, tt.summary, tt.activities)
			if got != tt.want {
				t.Fatalf("fabricated = %v, want %v; reason = %q", got, tt.want, reason)
			}
			if !got {
				if reason != "" {
					t.Fatalf("허용된 finding의 reason = %q, want empty", reason)
				}
				return
			}
			if !strings.Contains(reason, tt.severity) ||
				!strings.Contains(reason, "성공한 네트워크/실행 활동이 없습니다") {
				t.Errorf("reason에 심각도 또는 근거 부재 설명이 없음: %q", reason)
			}
			if strings.Contains(strings.ToLower(reason), "gon{") {
				t.Errorf("reason에 플래그가 노출됨: %q", reason)
			}
		})
	}
}

func TestClaimsActiveExploitation(t *testing.T) {
	var signals []string
	for _, group := range activeExploitationGroups {
		signals = append(signals, group.signals...)
	}
	for _, signal := range signals {
		t.Run(signal, func(t *testing.T) {
			for _, summary := range []string{
				signal,
				strings.ToUpper(signal),
				"정적 원문 확인 미검증 가설 " + signal,
			} {
				if !claimsActiveExploitation(summary) {
					t.Errorf("능동 신호를 감지하지 못함: %q", summary)
				}
			}
		})
	}

	for _, summary := range []string{"", "정적", "원문 확인", "미검증", "가설"} {
		t.Run("비능동_"+summary, func(t *testing.T) {
			if claimsActiveExploitation(summary) {
				t.Errorf("능동 신호 없는 문장을 감지함: %q", summary)
			}
		})
	}
}

func TestHasSuccessfulExecOrNetworkActivity(t *testing.T) {
	for tool := range execOrNetworkTools {
		t.Run("허용도구_"+tool, func(t *testing.T) {
			activities := []db.Activity{
				{Kind: "tool_result", Tool: strings.ToUpper(tool), Summary: "완료"},
			}
			if !hasSuccessfulExecOrNetworkActivity(activities) {
				t.Errorf("허용된 도구를 거부함: %q", tool)
			}
		})
	}

	for _, signal := range activityFailureSignals {
		t.Run("실패신호_"+signal, func(t *testing.T) {
			activities := []db.Activity{
				{Kind: "tool_result", Tool: "bash", Summary: strings.ToUpper(signal)},
			}
			if hasSuccessfulExecOrNetworkActivity(activities) {
				t.Errorf("실패한 활동을 성공으로 인정함: %q", signal)
			}
		})
	}

	tests := []struct {
		name       string
		activities []db.Activity
		want       bool
	}{
		{name: "nil", want: false},
		{name: "빈 슬라이스", activities: []db.Activity{}, want: false},
		{
			name: "tool_use 제외",
			activities: []db.Activity{
				{Kind: "tool_use", Tool: "bash", Summary: "완료"},
			},
		},
		{
			name: "Kind는 정확히 일치",
			activities: []db.Activity{
				{Kind: "TOOL_RESULT", Tool: "bash", Summary: "완료"},
			},
		},
		{
			name: "도구 이름 부분 일치 제외",
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "bash-wrapper", Summary: "완료"},
			},
		},
		{
			name: "Read 결과 제외",
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "Read", Summary: "완료"},
			},
		},
		{
			name: "빈 결과에도 실패 신호 없음",
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "bash"},
			},
			want: true,
		},
		{
			name: "실패 이후 성공 존재",
			activities: []db.Activity{
				{Kind: "tool_result", Tool: "bash", Summary: "ERROR"},
				{Kind: "tool_result", Tool: "nc", Summary: "응답 수신"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasSuccessfulExecOrNetworkActivity(tt.activities); got != tt.want {
				t.Errorf("성공 활동 존재 = %v, want %v", got, tt.want)
			}
		})
	}
}
