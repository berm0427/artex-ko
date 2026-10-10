package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestProveGoalRejectsInheritedEvidence(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	source, err := d.CreateTaskWithOptions("prove goal source", "source task", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(source.ID)
	current, err := d.CreateTaskWithOptions("prove goal current", "current task", db.TaskCreateOptions{SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(current.ID)

	sourceStore := d.Exploration(source.ExplorationID)
	factID, err := sourceStore.AddNode(db.KindFact, map[string]any{"summary": "HTTP request succeeded"}, 5, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	store := d.Exploration(current.ExplorationID)
	goalID, err := store.AddGoal(map[string]any{"text": "Verify the target"}, "human")
	if err != nil {
		t.Fatal(err)
	}

	tool := NewToolSet(store, "planner").proveGoal()
	result, err := tool.Call(context.Background(), json.RawMessage(fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"HTTP success"}`, goalID, factID)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Flatten(), "연관 작업에서 상속한") {
		t.Fatalf("expected inherited-evidence rejection, got %q", result.Flatten())
	}
	goal, err := store.GetNode(goalID)
	if err != nil || goal == nil || goal.State != "open" {
		t.Fatalf("inherited evidence changed goal state: goal=%+v err=%v", goal, err)
	}
}
