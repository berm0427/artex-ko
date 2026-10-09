package agent

import (
	"strings"
	"testing"
)

func TestWorkerOverviewOmitsDuplicateTaskAndCapsContext(t *testing.T) {
	facts := make([]map[string]any, 8)
	for i := range facts {
		facts[i] = map[string]any{"summary": "fact" + string(rune('0'+i))}
	}
	got := renderWorkerGraphOverview(map[string]any{
		"task":            map[string]any{"description": "TASK_DUPLICATE_SENTINEL"},
		"goals":           []map[string]any{{"text": "GOAL_DUPLICATE_SENTINEL"}},
		"running_intents": []map[string]any{{"summary": "INTENT_DUPLICATE_SENTINEL"}},
		"recent_facts":    facts,
	})
	for _, unwanted := range []string{"TASK_DUPLICATE_SENTINEL", "GOAL_DUPLICATE_SENTINEL", "INTENT_DUPLICATE_SENTINEL", "fact5", "fact6", "fact7"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("worker context includes duplicate/excess material %q: %s", unwanted, got)
		}
	}
	if !strings.Contains(got, "fact0") || !strings.Contains(got, "fact4") {
		t.Fatalf("worker context lost useful recent facts: %s", got)
	}
	if got := renderWorkerGraphOverview(map[string]any{"task": "repeat"}); got != "" {
		t.Fatalf("no relevant facts should yield no overview, got %q", got)
	}
}
