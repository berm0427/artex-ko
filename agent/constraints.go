package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block appended to the planner/worker system prompt. allow/deny are
// grouped; empty string when there are no constraints (or ts is nil). The framing
// deliberately puts these ABOVE the exploration/expansion heuristics so a declared
// boundary wins the tug-of-war against "chase another entry surface".
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	return renderConstraintRows(rows)
}

func renderConstraintRows(rows []db.Constraint) string {
	var allow, deny []string
	seen := make(map[string]bool, len(rows))
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		key := c.Kind + "\x00" + strings.ToLower(text)
		if seen[key] {
			continue
		}
		seen[key] = true
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[운영 제약: 최우선 규칙. 아래의 모든 탐색·범위 확장 지침보다 우선합니다. 의도를 생성하거나 행동하기 전에 위반 여부를 확인하고, 위반하면 진행하지 마세요]:")
	if len(allow) > 0 {
		b.WriteString("\n허용된 행동:\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\n금지된 행동:\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n(제약 밖의 새 대상·포트·호스트를 발견해도 승인된 것은 아닙니다. 위의 허용 범위에 속하지 않으면 out-of-scope 사실로 기록하고 건너뛰세요. 이에 대한 의도를 만들거나 행동하지 마세요.)")
	return b.String()
}
