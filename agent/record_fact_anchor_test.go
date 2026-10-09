package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRecordFactRejectsUnknownAssetBeforeWriting(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("record fact anchor test", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddIntent(map[string]any{"summary": "observe"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	tool := NewToolSet(store, "worker")
	tool.SetTaskID(task.ID)
	tool.SetAssetStore(d.Assets(), d.Assets().Companies())
	before, err := store.ListByKind(db.KindFact, 100)
	if err != nil {
		t.Fatal(err)
	}
	intentRaw, _ := json.Marshal(intentID)
	_, err = tool.recordOneFact(factItem{
		Summary: "observed response", IntentID: intentRaw,
		AssetIDs: []json.RawMessage{json.RawMessage(`9223372036854775807`)},
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "asset_ids") {
		t.Fatalf("expected actionable asset_ids error, got %v", err)
	}
	after, err := store.ListByKind(db.KindFact, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("rejected fact persisted: before=%d after=%d", len(before), len(after))
	}
}
