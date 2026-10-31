package agent

import (
	"testing"

	"github.com/Autumn-27/artex/db"
)

// tu builds a planner tool_use activity with the given id/tool/summary.
func tu(id int64, worker, tool, summary string) db.Activity {
	return db.Activity{ID: id, Worker: worker, Kind: "tool_use", Tool: tool, Summary: summary}
}

// tuid is tu with a ToolUseID, so its outcome can be paired with a tool_result row.
func tuid(id int64, worker, tool, summary, useID string) db.Activity {
	a := tu(id, worker, tool, summary)
	a.ToolUseID = useID
	return a
}

// trErr is the rejected/failed result row (tool_result, is_error=true) for a call.
func trErr(id int64, worker, tool, useID string) db.Activity {
	return db.Activity{ID: id, Worker: worker, Kind: "tool_result", Tool: tool, IsError: true, ToolUseID: useID}
}

func TestDetectCrossRoundStall(t *testing.T) {
	tests := []struct {
		name       string
		activities []db.Activity
		wantTool   string
		wantStall  bool
	}{
		{
			name: "three identical planner reads → stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {}`),
				tu(4, "planner", "list_intents", `list_intents {}`),
				tu(6, "planner", "list_intents", `list_intents {}`),
			},
			wantTool:  "list_intents",
			wantStall: true,
		},
		{
			name: "evidence lands within the streak → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {}`),
				tu(4, "planner", "list_intents", `list_intents {}`),
				tu(5, "worker", "record_fact", `record_fact {"summary":"new fact"}`),
				tu(6, "planner", "list_intents", `list_intents {}`),
			},
		},
		{
			name: "below threshold → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {}`),
				tu(4, "planner", "list_intents", `list_intents {}`),
			},
		},
		{
			name: "worker repeats are out of planner scope → not a stall",
			activities: []db.Activity{
				tu(2, "worker", "list_intents", `list_intents {}`),
				tu(4, "worker", "list_intents", `list_intents {}`),
				tu(6, "worker", "list_intents", `list_intents {}`),
			},
		},
		{
			name: "different arguments each time → not identical → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {"after":10}`),
				tu(4, "planner", "list_intents", `list_intents {"after":20}`),
				tu(6, "planner", "list_intents", `list_intents {"after":30}`),
			},
		},
		{
			name: "successful add_intent resets streak → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {}`),
				tu(4, "planner", "list_intents", `list_intents {}`),
				tu(6, "planner", "add_intent", `add_intent {"summary":"new"}`),
				tu(8, "planner", "list_intents", `list_intents {}`),
			},
		},
		{
			name: "alternating blind inventory polls across tools → stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {}`),
				tu(4, "planner", "list_facts", `list_facts {}`),
				tu(6, "planner", "list_findings", `list_findings {}`),
			},
			wantTool:  "list_findings",
			wantStall: true,
		},
		{
			name: "rejected duplicate add_intent does not reset streak → stall",
			activities: []db.Activity{
				tu(1, "planner", "add_intent", `add_intent {"summary":"a"}`),
				tu(2, "planner", "list_intents", `list_intents {}`),
				tu(4, "planner", "list_facts", `list_facts {}`),
				tuid(6, "planner", "add_intent", `add_intent {"summary":"dup"}`, "call_6"),
				trErr(7, "planner", "add_intent", "call_6"),
				tu(8, "planner", "list_intents", `list_intents {}`),
				tu(10, "planner", "list_facts", `list_facts {}`),
			},
			wantTool:  "list_facts",
			wantStall: true,
		},
		{
			name: "successful evidence has non-error tool_result → still resets → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {}`),
				tu(4, "planner", "list_facts", `list_facts {}`),
				tuid(6, "planner", "add_intent", `add_intent {"summary":"new"}`, "call_6"),
				{ID: 7, Worker: "planner", Kind: "tool_result", Tool: "add_intent", IsError: false, ToolUseID: "call_6"},
				tu(8, "planner", "list_intents", `list_intents {}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTool, gotStall := detectCrossRoundStall(tt.activities)
			if gotTool != tt.wantTool || gotStall != tt.wantStall {
				t.Fatalf(
					"detectCrossRoundStall() = (%q, %v), want (%q, %v)",
					gotTool, gotStall, tt.wantTool, tt.wantStall,
				)
			}
		})
	}
}

func TestPlannerIsLocalModelGating(t *testing.T) {
	if p := NewPlanner(nil, "artex-qwen3-4b-x", "", nil, 20_000, 0); !p.isLocalModel() {
		t.Fatalf("artex-qwen3 model should be local")
	}
	if p := NewPlanner(nil, "gpt-4o", "", nil, 20_000, 0); p.isLocalModel() {
		t.Fatalf("hosted model should not be local")
	}
	routed := NewPlanner(nil, "task-router", "", nil, 20_000, 0)
	routed.SetLocalModel(func() bool { return true })
	if !routed.isLocalModel() {
		t.Fatalf("router-hidden local model should be local")
	}
}

func TestPlannerBreakerEnabled(t *testing.T) {
	// Hosted model, no local-adapter resolver → breaker off.
	hosted := NewPlanner(nil, "task-router", "", nil, 20_000, 0)
	if hosted.breakerEnabled() {
		t.Fatalf("hosted model with no local signal should not enable the breaker")
	}
	// Untagged local adapter (e.g. artex-agentic-v34) is NOT isLocalModel but the
	// broader local-adapter resolver must still enable the breaker.
	adapter := NewPlanner(nil, "task-router", "", nil, 20_000, 0)
	if adapter.isLocalModel() {
		t.Fatalf("precondition: task-router without localModelFn is not isLocalModel")
	}
	adapter.SetLocalAdapter(func() bool { return true })
	if !adapter.breakerEnabled() {
		t.Fatalf("local adapter should enable the breaker")
	}
	// A tagged/router-hidden local model enables it via isLocalModel alone.
	tagged := NewPlanner(nil, "artex-qwen3-4b-x", "", nil, 20_000, 0)
	if !tagged.breakerEnabled() {
		t.Fatalf("qwen3-tagged model should enable the breaker")
	}
}
