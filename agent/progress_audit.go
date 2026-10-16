package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// recentProgressAudit gives each new planning/worker run a small, evidence-based
// account of repeated actions. It deliberately does not classify a service or
// prescribe a vulnerability: the same check works for web, TCP, files and code.
// Tool arguments may contain credentials, so only names and activity IDs leave
// this function; the agent can inspect an individual trace when it needs detail.
func recentProgressAudit(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	activities, _, err := ts.ActivityPage(db.ActivitySessionFilter{}, 0, 600)
	if err != nil {
		return ""
	}
	return renderProgressAudit(activities)
}

type repeatedAction struct {
	tool string
	ids  []int64
}

func renderProgressAudit(activities []db.Activity) string {
	groups := map[string]*repeatedAction{}
	var conclusions []db.Activity
	for _, a := range activities {
		if a.NodeID == nil { // exclude planner and main-agent bookkeeping
			continue
		}
		switch a.Kind {
		case "tool_use":
			if a.Tool == "" || a.Summary == "" {
				continue
			}
			key := a.Tool + "\x00" + a.Summary
			g := groups[key]
			if g == nil {
				g = &repeatedAction{tool: a.Tool}
				groups[key] = g
			}
			g.ids = append(g.ids, a.ID)
		case "result":
			if strings.TrimSpace(a.Summary) != "" {
				conclusions = append(conclusions, a)
			}
		}
	}
	var repeats []*repeatedAction
	for _, g := range groups {
		if len(g.ids) >= 3 {
			repeats = append(repeats, g)
		}
	}
	sort.Slice(repeats, func(i, j int) bool {
		if len(repeats[i].ids) != len(repeats[j].ids) {
			return len(repeats[i].ids) > len(repeats[j].ids)
		}
		return repeats[i].ids[len(repeats[i].ids)-1] > repeats[j].ids[len(repeats[j].ids)-1]
	})
	if len(repeats) == 0 && len(conclusions) < 3 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【최근 실행의 진전 점검(도구 기록에서 자동 계산)】")
	if len(repeats) > 0 {
		b.WriteString("\n동일한 도구·인자 호출이 3회 이상 반복됐습니다. 성공한 동일 검사를 다시 수행하기 전에 새 입력·대상·가설·관찰 기준 중 무엇이 달라졌는지 확인하세요:")
		for _, g := range repeats[:min(len(repeats), 5)] {
			fmt.Fprintf(&b, "\n- %s %d회 (최근 활동 ID %d; 상세는 해당 실행 기록 참조)", g.tool, len(g.ids), g.ids[len(g.ids)-1])
		}
	}
	if len(conclusions) >= 3 {
		b.WriteString("\n최근 종료된 실행 의도들의 결론:")
		for _, a := range conclusions[max(0, len(conclusions)-3):] {
			fmt.Fprintf(&b, "\n- #%d: %s", a.ID, truncOutput(strings.TrimSpace(a.Summary), 180))
		}
		b.WriteString("\n목표에 가까워진 새 증거가 없다면 기존의 연결·버전·권한 확인을 다시 배정하지 말고, 제공 자료와 관찰에 근거한 다른 원리의 검증 가능한 가설을 세우세요. 가능하면 격리된 로컬 재현으로 먼저 판별하고, 범위·제약을 지킨 뒤 원격 검증을 결정하세요. 새 가설을 만들 수 없다면 한계를 명시하세요.")
	}
	return b.String()
}
