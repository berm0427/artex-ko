package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// Run explicitly with an isolated ARTEX_PG_DSN and a local OpenAI-compatible
// model endpoint. The normal test suite must not contact a live model.
func TestLocalModelGoalFlow(t *testing.T) {
	if os.Getenv("ARTEX_LOCAL_MODEL_LAB") != "1" {
		t.Skip("opt-in local model lab test")
	}
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Fatal("ARTEX_PG_DSN must point to an isolated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	fixtureCalls := make(chan struct{}, 4)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		select {
		case fixtureCalls <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer fixture.Close()
	profileID, err := m.pg.SaveProfile(&db.LLMProfile{
		Name: "Local Qwen lab", Format: "openai", BaseURL: "http://127.0.0.1:1234/v1",
		Model: "qwen/qwen3-8b", APIKey: "lm-studio", Streaming: true, MaxTokens: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pg.SetActiveProfile(profileID); err != nil {
		t.Fatal(err)
	}
	toolExec, _ := json.Marshal(map[string]any{"method": "GET", "url": fixture.URL + "/health"})
	if err := m.pg.CreateCustomTool(&db.Tool{
		Key: "lab_health", Description: "로컬 실습 서버의 /health 상태만 읽습니다. 확인된 응답만 기록하세요.",
		Schema: json.RawMessage(`{"type":"object","properties":{"check":{"type":"string"}}}`),
		Agents: []string{"worker"}, Enabled: true, Kind: "http", Exec: toolExec,
	}); err != nil {
		t.Fatal(err)
	}
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	task, err := m.CreateTask("로컬 상태 확인 실습", "로컬 실습 서버의 상태를 확인하고 확인된 사실만 기록한다", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	goals := s.createGoals(ctx, task, nil)
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	if len(goals) == 0 {
		t.Fatal("no goals produced")
	}
	nodes, err := task.Store.ListByKind(db.KindGoal, 100)
	if err != nil {
		t.Fatal(err)
	}
	fromModel := false
	for _, node := range nodes {
		if node.Origin == "goals" {
			fromModel = true
		}
	}
	if !fromModel {
		t.Fatalf("model did not call set_goals; fallback-only goals: %+v", goals)
	}
	t.Logf("model created %d goal(s) through set_goals", len(goals))

	// The lab harness exposes only a fixed localhost reader and fact recording to
	// the worker. This does not change production tool bindings or behavior.
	previousResolve := agent.ToolResolve
	agent.ToolResolve = func(ctx context.Context, key string, tools []actool.CoreTool) []actool.CoreTool {
		resolved := previousResolve(ctx, key, tools)
		if key != "worker" {
			return resolved
		}
		var safe []actool.CoreTool
		for _, tool := range resolved {
			if tool.Name() == "lab_health" || tool.Name() == "record_fact" {
				safe = append(safe, tool)
			}
		}
		return safe
	}
	defer func() { agent.ToolResolve = previousResolve }()
	if _, err := m.pg.Exec(`UPDATE agents SET max_turns=5 WHERE key='worker'`); err != nil {
		t.Fatal(err)
	}
	intentID, err := task.Store.AddIntent(map[string]any{"summary": "lab_health 도구로 로컬 /health 응답을 확인하고 정상 상태를 record_fact로 기록한다. 취약점은 추정하지 않는다."}, 8, nil, "lab")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := task.Store.GetNode(intentID)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	_, writes, err := s.agentsForTask(task).wk.Execute(ctx, "work#1", taskID, m.Assets(), task.Store, intent, nil, func(a db.Activity) {
		if _, writeErr := task.Store.AppendActivity(a); writeErr != nil {
			t.Errorf("persist worker activity: %v", writeErr)
		}
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtureCalls) == 0 {
		t.Fatal("worker did not call the fixed local HTTP tool")
	}
	if writes.Facts == 0 || writes.Findings != 0 {
		t.Fatalf("expected a normal-state fact and no vulnerability: %+v", writes)
	}
	facts, err := task.Store.ListByKind(db.KindFact, 100)
	if err != nil {
		t.Fatal(err)
	}
	observedResponse := false
	for _, fact := range facts {
		body := string(fact.Payload)
		if strings.Contains(body, "200") && strings.Contains(body, "ok") {
			observedResponse = true
		}
	}
	if !observedResponse {
		t.Fatal("recorded facts do not contain the observed 200/ok response")
	}
	t.Logf("worker called local fixture and recorded %d fact(s), %d finding(s)", writes.Facts, writes.Findings)
}

// This opt-in test asks the real local model to verify a synthetic ownership
// bypass, then checks that ARTEX persisted a finding backed by a tool response.
func TestLocalModelIDORFlow(t *testing.T) {
	if os.Getenv("ARTEX_LOCAL_MODEL_LAB") != "1" {
		t.Skip("opt-in local model lab test")
	}
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Fatal("ARTEX_PG_DSN must point to an isolated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	modelID := os.Getenv("ARTEX_LOCAL_MODEL_ID")
	if modelID == "" {
		modelID = "qwen/qwen3-8b"
	}
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("X-Lab-User")
		if user != "alice" && user != "bob" {
			http.Error(w, "unknown lab user", 401)
			return
		}
		if r.URL.Path == "/api/invoices/INV-1001" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"INV-1001","owner":"alice","marker":"ALICE-LOCAL-LAB"}`))
			return
		}
		if r.URL.Path == "/api/invoices/INV-1002" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"INV-1002","owner":"bob","marker":"BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer fixture.Close()
	profileID, err := m.pg.SaveProfile(&db.LLMProfile{Name: "Local IDOR lab", Format: "openai", BaseURL: "http://127.0.0.1:1234/v1", Model: modelID, APIKey: "lm-studio", Streaming: false, MaxTokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pg.SetActiveProfile(profileID); err != nil {
		t.Fatal(err)
	}
	toolExec, _ := json.Marshal(map[string]any{"method": "GET", "url": fixture.URL + "/api/invoices/{invoice_id}", "headers": map[string]string{"X-Lab-User": "{user}"}})
	if err := m.pg.CreateCustomTool(&db.Tool{Key: "lab_invoice", Description: "Fetch one synthetic invoice by invoice_id and user identity. Lab policy: an invoice is private to its owner; Alice must not read Bob's invoice. Test Alice's own INV-1001 and Bob's INV-1002 with user=alice.", Schema: json.RawMessage(`{"type":"object","properties":{"invoice_id":{"type":"string"},"user":{"type":"string"}},"required":["invoice_id","user"]}`), Agents: []string{"worker"}, Enabled: true, Kind: "http", Exec: toolExec}); err != nil {
		t.Fatal(err)
	}
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	task, err := m.CreateTask("Local synthetic IDOR validation", "Verify whether Alice can read Bob's invoice from the local synthetic lab and record a finding only if observed", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	previousResolve := agent.ToolResolve
	agent.ToolResolve = func(ctx context.Context, key string, tools []actool.CoreTool) []actool.CoreTool {
		resolved := previousResolve(ctx, key, tools)
		if key != "worker" {
			return resolved
		}
		var safe []actool.CoreTool
		for _, tool := range resolved {
			if tool.Name() == "lab_invoice" || tool.Name() == "record_fact" || tool.Name() == "report_finding" {
				safe = append(safe, tool)
			}
		}
		return safe
	}
	defer func() { agent.ToolResolve = previousResolve }()
	if _, err := m.pg.Exec(`UPDATE agents SET max_turns=8 WHERE key='worker'`); err != nil {
		t.Fatal(err)
	}
	intentID, err := task.Store.AddIntent(map[string]any{"summary": "Lab policy: each invoice is private to its owner. Use lab_invoice with user=alice for INV-1001 and INV-1002. Alice should be denied Bob's invoice. Compare HTTP status and owner fields. If Alice gets HTTP 200 with Bob's private data, report IDOR using report_finding, intent_id, and evidence_quote copied verbatim from the successful tool result (the complete BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB marker is a valid excerpt). Do not report a finding if access is denied or requests fail."}, 8, nil, "lab")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := task.Store.GetNode(intentID)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	_, writes, err := s.agentsForTask(task).wk.Execute(ctx, "idor#1", taskID, m.Assets(), task.Store, intent, nil, func(a db.Activity) {
		if _, writeErr := task.Store.AppendActivity(a); writeErr != nil {
			t.Errorf("persist worker activity: %v", writeErr)
		}
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Findings == 0 {
		t.Fatalf("model did not record an IDOR finding: %+v", writes)
	}
	findings, err := task.Store.ListByKind(db.KindFinding, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("finding count is nonzero but graph has no finding")
	}
	found := false
	for _, finding := range findings {
		if strings.Contains(string(finding.Payload), "BOB-CONFIDENTIAL-INVOICE-LOCAL-LAB") {
			found = true
		}
	}
	if !found {
		t.Fatalf("finding does not contain Bob's observed marker: %+v", findings)
	}
	t.Logf("model recorded %d finding(s) with observed Bob invoice evidence", writes.Findings)
}
