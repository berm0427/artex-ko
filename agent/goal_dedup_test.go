package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestSetGoalsReusesDuplicateWithinAndAcrossCalls(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("goal dedup", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	tool := NewToolSet(d.Exploration(task.ExplorationID), "goals").setGoals()
	input := json.RawMessage(`{"goals":[{"text":"verify local XSS","vulnclass":"XSS"},{"text":" verify   local XSS ","vulnclass":"xss"}]}`)
	for i := 0; i < 2; i++ {
		if _, err := tool.Call(context.Background(), input, nil); err != nil {
			t.Fatal(err)
		}
	}
	goals, err := d.Exploration(task.ExplorationID).ListByKind(db.KindGoal, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 {
		t.Fatalf("same goal stored %d times", len(goals))
	}
}

func TestFactWithoutEvidenceCannotProveVulnerabilityGoal(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("unverified fact", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddIntent(map[string]any{"summary": "observe"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	goalID, err := store.AddGoal(map[string]any{"text": "verify XSS", "vulnclass": "XSS"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	tool := NewToolSet(store, "worker")
	intentRaw, _ := json.Marshal(intentID)
	factID, err := tool.recordOneFact(factItem{Summary: "model says no injection", IntentID: intentRaw}, 0)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := store.GetNode(factID)
	if err != nil {
		t.Fatal(err)
	}
	if fact.State != "reported" {
		t.Fatalf("unsupported fact state = %q", fact.State)
	}
	proof := NewToolSet(store, "planner").proveGoal()
	res, err := proof.Call(context.Background(), json.RawMessage(fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d}`, goalID, factID)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Flatten(), "finding") {
		t.Fatalf("vulnerability goal accepted ordinary fact: %q", res.Flatten())
	}
	goal, err := store.GetNode(goalID)
	if err != nil || goal.State != "open" {
		t.Fatalf("goal marked met: %+v, %v", goal, err)
	}
}

func TestGoalMetCannotBypassOpenGoal(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("goal bypass", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	if _, err := store.AddGoal(map[string]any{"text": "verify XSS", "vulnclass": "XSS"}, "test"); err != nil {
		t.Fatal(err)
	}
	toolSet := NewToolSet(store, "planner")
	res, err := toolSet.goalMet().Call(context.Background(), json.RawMessage(`{"reason":"all done"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if toolSet.GoalMet || !strings.Contains(res.Flatten(), "우회") {
		t.Fatalf("open goal bypassed: goalMet=%v result=%q", toolSet.GoalMet, res.Flatten())
	}
}
