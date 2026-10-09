package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRecoverFindingQuoteFromName(t *testing.T) {
	name, quote := recoverFindingQuoteFromName("IDOR 취약점 발견', 'evidence_quote': 'BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB'")
	if name != "IDOR 취약점 발견" || quote != "BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB" {
		t.Fatalf("name=%q quote=%q", name, quote)
	}
	name, quote = recoverFindingQuoteFromName("ordinary finding")
	if name != "ordinary finding" || quote != "" {
		t.Fatalf("unexpected recovery: name=%q quote=%q", name, quote)
	}
}

func TestObservedFactRequiresVerbatimSuccessfulToolOutput(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("evidence provenance", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddIntent(map[string]any{"summary": "check response"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendActivity(db.Activity{NodeID: &intentID, Kind: "tool_result", Tool: "Bash", IsError: true, Detail: "ERROR: pretend-success-output"}); err != nil {
		t.Fatal(err)
	}
	successID, err := store.AppendActivity(db.Activity{NodeID: &intentID, Kind: "tool_result", Tool: "Bash", Detail: "HTTP/1.1 200 OK\nreal-response-body-marker"})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewToolSet(store, "worker")
	intentRaw, _ := json.Marshal(intentID)
	for _, tc := range []struct{ quote, state string }{
		{"pretend-success-output", "reported"},
		{"made-up-response-body", "reported"},
		{"real-response-body-marker", "confirmed"},
	} {
		id, err := tool.recordOneFact(factItem{Summary: "observation", IntentID: intentRaw, EvidenceQuote: tc.quote}, 0)
		if err != nil {
			t.Fatal(err)
		}
		node, err := store.GetNode(id)
		if err != nil || node.State != tc.state {
			t.Fatalf("quote %q: node=%+v err=%v", tc.quote, node, err)
		}
		if tc.state == "confirmed" {
			var payload map[string]any
			_ = json.Unmarshal(node.Payload, &payload)
			if int64(payload["evidence_activity_id"].(float64)) != successID {
				t.Fatalf("wrong evidence activity: %s", node.Payload)
			}
		}
	}
}

func TestFindingRejectsUnmatchedEvidenceQuote(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("finding provenance", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddIntent(map[string]any{"summary": "check response"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendActivity(db.Activity{NodeID: &intentID, Kind: "tool_result", Tool: "Bash", Detail: "HTTP/1.1 200 OK\nreal-response-body-marker"}); err != nil {
		t.Fatal(err)
	}
	toolSet := NewToolSet(store, "worker")
	toolSet.SetTaskID(task.ID)
	toolSet.SetOwnerNode(intentID)
	tool := toolSet.addFinding()
	withoutIntent, err := tool.Call(context.Background(), json.RawMessage(`{"vulnclass":"test","severity":"low","summary":"local fixture"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withoutIntent.Flatten(), "intent_id") {
		t.Fatalf("missing intent bypassed evidence check: %q", withoutIntent.Flatten())
	}
	input := fmt.Sprintf(`{"intent_id":%d,"vulnclass":"test","severity":"low","summary":"local fixture","evidence":"request and response","evidence_quote":"made-up-response-body"}`, intentID)
	res, err := tool.Call(context.Background(), json.RawMessage(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Flatten(), "evidence_quote") {
		t.Fatalf("unmatched quote accepted: %q", res.Flatten())
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1`, task.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unmatched quote persisted %d findings", count)
	}
	valid := fmt.Sprintf(`{"intent_id":%d,"vulnclass":"test","severity":"low","summary":"local fixture","evidence":"request and response","evidence_quote":"real-response-body-marker"}`, intentID)
	accepted, err := tool.Call(context.Background(), json.RawMessage(valid), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(accepted.Flatten(), "finding recorded:") {
		t.Fatalf("matching quote rejected: %q", accepted.Flatten())
	}
	if err := d.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1`, task.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("matching quote persisted %d findings", count)
	}
}
