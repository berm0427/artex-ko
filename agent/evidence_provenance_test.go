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
	successID, err := store.AppendActivity(db.Activity{NodeID: &intentID, Kind: "tool_result", Tool: "Bash", Detail: "HTTP/1.1 200 OK\nDate: Sat, 10 Oct 2026 05:17:47 GMT\nreal-response-body-marker\n<title>자료실</title>"})
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
	id, err := tool.recordOneFact(factItem{Summary: "verbatim evidence field", IntentID: intentRaw, Evidence: "real-response-body-marker"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.GetNode(id)
	if err != nil || node.State != "confirmed" {
		t.Fatalf("verbatim evidence fallback: node=%+v err=%v", node, err)
	}
	id, err = tool.recordOneFact(factItem{Summary: "non-adjacent output lines", IntentID: intentRaw, Evidence: "HTTP/1.1 200 OK\nreal-response-body-marker"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	node, err = store.GetNode(id)
	if err != nil || node.State != "confirmed" {
		t.Fatalf("matching evidence line fallback: node=%+v err=%v", node, err)
	}
	id, err = tool.recordOneFact(factItem{Summary: "abbreviated evidence", IntentID: intentRaw, Evidence: "curl.exe -i -sS http://127.0.0.1:9091/ => HTTP/1.1 200 OK ... <title>자료실</title>..."}, 0)
	if err != nil {
		t.Fatal(err)
	}
	node, err = store.GetNode(id)
	if err != nil || node.State != "confirmed" {
		t.Fatalf("matching abbreviated evidence segment: node=%+v err=%v", node, err)
	}
}

func TestRoutineHTTPMetadataIsNotFindingProof(t *testing.T) {
	for _, tc := range []struct {
		quote string
		want  bool
	}{
		{"< HTTP/1.0 200 OK\n< Server: BaseHTTP/0.6 Python/3.14.0\n< Content-Type: text/html; charset=utf-8", true},
		{"* Established connection to 127.0.0.1 (127.0.0.1 port 9091)", false},
		{"HTTP/1.1 200 OK\nX-Api-Key: SECRET-MARKER", false},
		{"HTTP/1.1 200 OK\nContent-Type: text/html\nBOB-CONFIDENTIAL-INVOICE-LOCAL-LAB", false},
	} {
		if got := routineHTTPMetadataOnly(tc.quote); got != tc.want {
			t.Errorf("quote %q: got %v, want %v", tc.quote, got, tc.want)
		}
	}
	if !publicPageExposureClaim("information_disclosure", "HTTP 응답 헤더와 본문 내용이 공개됨", "공개 문서를 다운로드할 수 있습니다") {
		t.Fatal("public-page exposure claim should be rejected")
	}
	if publicPageExposureClaim("information_disclosure", "비인가 사용자의 민감 정보 노출", "BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB") {
		t.Fatal("specific sensitive-data claim should receive ordinary evidence review")
	}
}

func TestDeniedAccessEvidence(t *testing.T) {
	for _, tc := range []struct {
		quote string
		want  bool
	}{
		{"Outside lab boundary", true},
		{"HTTP/1.1 403 Forbidden", true},
		{"File not found", true},
		{"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB", false},
	} {
		if got := deniedAccessEvidence(tc.quote); got != tc.want {
			t.Errorf("quote %q: got %v, want %v", tc.quote, got, tc.want)
		}
	}
}

func TestFindingRejectsDeniedAccessOutput(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("denied download evidence", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddIntent(map[string]any{"summary": "check download"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	quote := "Outside lab boundary"
	if _, err := store.AppendActivity(db.Activity{NodeID: &intentID, Kind: "tool_result", Tool: "Bash", Detail: quote}); err != nil {
		t.Fatal(err)
	}
	toolSet := NewToolSet(store, "worker")
	toolSet.SetTaskID(task.ID)
	toolSet.SetOwnerNode(intentID)
	body, _ := json.Marshal(map[string]any{"intent_id": intentID, "vulnclass": "IDOR", "severity": "high", "summary": "비공개 파일 다운로드 성공", "evidence_quote": quote})
	res, err := toolSet.addFinding().Call(t.Context(), body, nil)
	if err != nil || !strings.Contains(res.Flatten(), "취약점 미등록") {
		t.Fatalf("denied response was not rejected: %v %q", err, res.Flatten())
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1`, task.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied response persisted finding: count=%d err=%v", count, err)
	}
}

func TestFindingAcceptsVerbatimFragmentFromJoinedQuote(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("joined finding evidence", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddIntent(map[string]any{"summary": "check canary"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	marker := "LAB-ONLY-CANARY: access to this document from the public download endpoint demonstrates path traversal."
	if _, err := store.AppendActivity(db.Activity{NodeID: &intentID, Kind: "tool_result", Tool: "Bash", Detail: "HTTP/1.0 200 OK\r\nContent-Length: 152\r\n\r\n" + marker}); err != nil {
		t.Fatal(err)
	}
	toolSet := NewToolSet(store, "worker")
	toolSet.SetTaskID(task.ID)
	toolSet.SetOwnerNode(intentID)
	body, _ := json.Marshal(map[string]any{"intent_id": intentID, "vulnclass": "Path Traversal", "severity": "high", "summary": "비공개 파일 노출", "evidence_quote": "HTTP/1.0 200 OK; " + marker + " This is model-added prose."})
	res, err := toolSet.addFinding().Call(t.Context(), body, nil)
	if err != nil || !strings.Contains(res.Flatten(), "finding recorded:") {
		t.Fatalf("verbatim marker was rejected: %v %q", err, res.Flatten())
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

func TestFindingRejectsRoutineHTTP200Evidence(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("routine HTTP evidence", "local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddIntent(map[string]any{"summary": "inspect public page"}, 1, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	quote := "HTTP/1.0 200 OK\nServer: BaseHTTP/0.6 Python/3.14.0\nContent-Type: text/html; charset=utf-8"
	if _, err := store.AppendActivity(db.Activity{NodeID: &intentID, Kind: "tool_result", Tool: "Bash", Detail: quote}); err != nil {
		t.Fatal(err)
	}
	toolSet := NewToolSet(store, "worker")
	toolSet.SetTaskID(task.ID)
	toolSet.SetOwnerNode(intentID)
	body, _ := json.Marshal(map[string]any{"intent_id": intentID, "vulnclass": "information_disclosure", "severity": "low", "summary": "HTTP 응답 헤더 공개", "evidence_quote": quote})
	res, err := toolSet.addFinding().Call(t.Context(), body, nil)
	if err != nil || !strings.Contains(res.Flatten(), "취약점 미등록") {
		t.Fatalf("routine response was not rejected: %v %q", err, res.Flatten())
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1`, task.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("routine response persisted finding: count=%d err=%v", count, err)
	}
}
