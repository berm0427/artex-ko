package agent

import (
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// 네트워크/실행 성공을 입증할 수 있는 도구 집합(소문자, 정확히 일치).
// Bash와 대화형 셸 세션(shell_open/shell_send)이 ARTEX의 실제 실행·연결 표면이다.
// nc/curl/redis-cli 등은 Bash 안에서 실행되므로 활동 도구명으로는 나타나지 않지만,
// 하위 호환과 가독성을 위해 함께 둔다(무해한 추가 항목).
var execOrNetworkTools = map[string]struct{}{
	"bash": {}, "shell_open": {}, "shell_send": {},
	"docker": {}, "redis-cli": {}, "nc": {}, "ncat": {},
	"socat": {}, "curl": {}, "python": {}, "python3": {}, "openssl": {}, "nmap": {},
}

// tool_result 요약에 하나라도 포함되면 실패로 간주하는 신호(소문자).
var activityFailureSignals = []string{
	"오류", "실패", "거부", "error", "failed", "refus",
	"timeout", "타임아웃", "no route", "cannot", "not found", "연결할 수 없",
}

// 능동 침해 주장 분류와 그 신호(소문자). 반환되는 category는 고정 문구이므로 실제 플래그 값은 노출되지 않는다.
var activeExploitationGroups = []struct {
	category string
	signals  []string
}{
	{"플래그 누출 또는 출력", []string{
		"gon{", "flag 누출", "flag 출력", "플래그 누출", "플래그 출력", "cat /", "/flag",
	}},
	{"원격 명령 실행 또는 셸 획득", []string{
		"get_shell", "shell", "셸", "원격 명령", "명령 실행", "command execution", "rce", "reverse shell",
	}},
	{"메모리 손상 익스플로잇", []string{
		"버퍼", "오버플로우", "overflow", "반환 주소", "return address", "덮어", "스택",
	}},
	{"구체적인 실행 결과 관찰", []string{
		"세그폴트", "segfault", "exit 139", "종료 코드 139", "코어 덤프",
	}},
}

// detectFabricatedActiveFinding 은 능동 공격 결과를 주장하는 finding 이
// 이를 뒷받침하는 성공한 네트워크/실행 활동 없이 등록되려 할 때 fabricated=true 를 반환한다.
func detectFabricatedActiveFinding(severity, summary string, activities []db.Activity) (reason string, fabricated bool) {
	category := activeExploitationCategory(summary)
	if category == "" || hasSuccessfulExecOrNetworkActivity(activities) {
		return "", false
	}
	return fmt.Sprintf(
		"심각도 %s: '%s' 주장이 감지됐으나 이를 뒷받침하는 성공한 네트워크/실행 활동이 없습니다.",
		severity, category,
	), true
}

// hasSuccessfulExecOrNetworkActivity 는 실패 신호 없는 네트워크/실행 tool_result 가
// 하나라도 있으면 true 를 반환한다. nil/빈 슬라이스에 안전하다.
func hasSuccessfulExecOrNetworkActivity(activities []db.Activity) bool {
	for _, activity := range activities {
		if activity.Kind != "tool_result" {
			continue
		}
		if _, ok := execOrNetworkTools[strings.ToLower(activity.Tool)]; !ok {
			continue
		}
		if !containsAny(strings.ToLower(activity.Summary), activityFailureSignals...) {
			return true
		}
	}
	return false
}

// claimsActiveExploitation 은 summary 가 능동 침해 결과를 주장하면 true 를 반환한다.
func claimsActiveExploitation(summary string) bool {
	return activeExploitationCategory(summary) != ""
}

// activeExploitationCategory 는 summary 가 주장하는 능동 침해 분류명을 반환한다. 없으면 "".
func activeExploitationCategory(summary string) string {
	normalized := strings.ToLower(summary)
	for _, group := range activeExploitationGroups {
		if containsAny(normalized, group.signals...) {
			return group.category
		}
	}
	return ""
}
