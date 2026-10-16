package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestPlannerRejectsIdenticalFinishedIntentButAllowsChangedQuestion(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("repeat audit", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	first, err := store.AddIntent(map[string]any{"summary": "inspect service behavior"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeState(first, "done"); err != nil {
		t.Fatal(err)
	}
	tools := NewToolSet(store, "planner")
	if _, err := tools.addOneIntent(intentItem{Summary: "  INSPECT  service behavior  "}); err == nil || !strings.Contains(err.Error(), "이미 존재") {
		t.Fatalf("repeated finished intent accepted: %v", err)
	}
	if _, err := tools.addOneIntent(intentItem{Summary: "inspect service behavior with a new response boundary"}); err != nil {
		t.Fatalf("changed verification question rejected: %v", err)
	}
}
