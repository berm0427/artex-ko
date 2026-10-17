package agent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestPlannerIntentHistorySearchPagesOlderWork(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("intent history", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	var first int64
	for _, summary := range []string{"sparse boundary case", "other experiment", "sparse control case"} {
		id, err := store.AddIntent(map[string]any{"summary": summary}, 5, nil, "planner")
		if err != nil {
			t.Fatal(err)
		}
		if first == 0 {
			first = id
		}
	}
	tool := NewToolSet(store, "planner").listIntents()
	page := callReadJSON(t, tool, `{"q":"sparse","limit":1}`).(map[string]any)
	if page["total"] != float64(2) || page["has_more"] != true {
		t.Fatalf("first page metadata: %#v", page)
	}
	second := callReadJSON(t, tool, `{"q":"sparse","limit":1,"before":`+
		strconv.FormatInt(int64(page["next_before"].(float64)), 10)+`}`).(map[string]any)
	items := second["intents"].([]any)
	if len(items) != 1 || int64(items[0].(map[string]any)["id"].(float64)) != first {
		t.Fatalf("older matching intent unavailable: %#v", second)
	}
}

func TestPlannerNoveltyRuleSurvivesStoredPromptOverride(t *testing.T) {
	t.Cleanup(func() { PromptOverride = nil })
	PromptOverride = func(k string) (string, bool) {
		if k == "planner" {
			return "OLD: always refill the frontier", true
		}
		return "", false
	}
	got := plannerSystem("goal", "/data", "/work")
	for _, want := range []string{"list_intents(q/before)", "add_intent를 호출하지 마세요", "중단 기준"} {
		if !strings.Contains(got, want) {
			t.Fatalf("code-owned novelty rule missing %q", want)
		}
	}
}
