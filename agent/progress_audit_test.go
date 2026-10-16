package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRenderProgressAuditDetectsRepeatedWorkerCallsWithoutLeakingArguments(t *testing.T) {
	id := int64(42)
	const private = "password=do-not-repeat"
	activities := []db.Activity{
		{ID: 1, NodeID: &id, Kind: "tool_use", Tool: "tcp_exchange", Summary: private},
		{ID: 2, NodeID: &id, Kind: "tool_use", Tool: "tcp_exchange", Summary: private},
		{ID: 3, NodeID: &id, Kind: "tool_use", Tool: "tcp_exchange", Summary: private},
		{ID: 4, Kind: "tool_use", Tool: "tcp_exchange", Summary: private}, // planner call excluded
	}
	got := renderProgressAudit(activities)
	if !strings.Contains(got, "tcp_exchange 3회") || !strings.Contains(got, "활동 ID 3") {
		t.Fatalf("missing repeat evidence: %s", got)
	}
	if strings.Contains(got, private) {
		t.Fatalf("tool arguments leaked into audit: %s", got)
	}
}

func TestRenderProgressAuditIncludesRecentConclusionsAndRequiresPivot(t *testing.T) {
	id := int64(42)
	activities := []db.Activity{
		{ID: 10, NodeID: &id, Kind: "result", Summary: "first observation"},
		{ID: 11, NodeID: &id, Kind: "result", Summary: "second observation"},
		{ID: 12, NodeID: &id, Kind: "result", Summary: "third observation"},
		{ID: 13, NodeID: &id, Kind: "result", Summary: "fourth observation"},
	}
	got := renderProgressAudit(activities)
	if strings.Contains(got, "first observation") || !strings.Contains(got, "fourth observation") || !strings.Contains(got, "격리된 로컬 재현") {
		t.Fatalf("expected latest three conclusions and pivot guidance: %s", got)
	}
	if got := renderProgressAudit(activities[:2]); got != "" {
		t.Fatalf("ordinary early exploration should not trigger audit: %s", got)
	}
}
