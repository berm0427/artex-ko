package agent

import (
	"testing"

	"github.com/Autumn-27/artex/db"
)

// tu builds a planner tool_use activity with the given id/tool/summary.
func tu(id int64, worker, tool, summary string) db.Activity {
	return db.Activity{ID: id, Worker: worker, Kind: "tool_use", Tool: tool, Summary: summary}
}

// tuErr is tu with IsError set — a rejected/failed tool call.
func tuErr(id int64, worker, tool, summary string) db.Activity {
	a := tu(id, worker, tool, summary)
	a.IsError = true
	return a
}

func TestDetectCrossRoundStall(t *testing.T) {
	const li = `list_intents {"before":0,"limit":20}`
	cases := []struct {
		name       string
		activities []db.Activity
		wantTool   string
		wantStall  bool
	}{
		{
			name: "three identical planner reads, no evidence after streak start → stall",
			activities: []db.Activity{
				tu(1, "planner", "add_intent", `add_intent {...}`), // evidence BEFORE the streak
				tu(2, "planner", "list_intents", li),
				tu(4, "planner", "graph_overview", `graph_overview {}`),
				tu(6, "planner", "list_intents", li),
				tu(8, "planner", "list_intents", li),
			},
			wantTool:  "list_intents",
			wantStall: true,
		},
		{
			name: "evidence lands within the streak → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", li),
				tu(4, "planner", "list_intents", li),
				tu(6, "planner", "record_fact", `record_fact {...}`), // new evidence after reads began
				tu(8, "planner", "list_intents", li),
			},
			wantStall: false,
		},
		{
			name: "below threshold → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", li),
				tu(4, "planner", "list_intents", li),
			},
			wantStall: false,
		},
		{
			name: "worker repeats are out of planner scope → not a stall",
			activities: []db.Activity{
				tu(2, "work#1", "list_assets", `list_assets {"dsl":"type=ip"}`),
				tu(4, "work#1", "list_assets", `list_assets {"dsl":"type=ip"}`),
				tu(6, "work#1", "list_assets", `list_assets {"dsl":"type=ip"}`),
			},
			wantStall: false,
		},
		{
			name: "different arguments each time → not identical → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {"before":0}`),
				tu(4, "planner", "list_intents", `list_intents {"before":40}`),
				tu(6, "planner", "list_intents", `list_intents {"before":80}`),
			},
			wantStall: false,
		},
		{
			name: "rejected duplicate add_intent does not reset streak → stall",
			activities: []db.Activity{
				tu(1, "planner", "add_intent", `add_intent {"title":"initial"}`), // successful evidence
				tu(2, "planner", "list_intents", `list_intents {"before":0,"limit":20}`),
				tu(4, "planner", "list_facts", `list_facts {}`),
				tuErr(6, "planner", "add_intent", `add_intent {"title":"initial"}`), // rejected dup: must NOT reset
				tu(8, "planner", "list_intents", `list_intents {"before":0,"limit":20}`),
				tu(10, "planner", "list_facts", `list_facts {}`),
			},
			wantTool:  "list_facts",
			wantStall: true,
		},
		{
			name: "successful add_intent resets streak → not a stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {"before":0,"limit":20}`),
				tu(4, "planner", "list_facts", `list_facts {}`),
				tu(6, "planner", "add_intent", `add_intent {"title":"new"}`), // real progress within streak
				tu(8, "planner", "list_intents", `list_intents {"before":0,"limit":20}`),
			},
			wantStall: false,
		},
		{
			name: "alternating blind inventory polls across tools → stall",
			activities: []db.Activity{
				tu(2, "planner", "list_intents", `list_intents {"before":0,"limit":20}`),
				tu(4, "planner", "list_facts", `list_facts {}`),
				tu(6, "planner", "list_findings", `list_findings {}`),
			},
			wantTool:  "list_findings",
			wantStall: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, stall := detectCrossRoundStall(c.activities)
			if stall != c.wantStall {
				t.Fatalf("stall = %v, want %v (tool=%q)", stall, c.wantStall, tool)
			}
			if c.wantStall && tool != c.wantTool {
				t.Fatalf("tool = %q, want %q", tool, c.wantTool)
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
