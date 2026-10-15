package agent

import (
	"strings"
	"testing"
)

func TestBinaryAnalysisToolGuidanceOnlyWhenREAAvailable(t *testing.T) {
	if got := binaryAnalysisToolGuidance(DeferredInfo{}); got != "" {
		t.Fatalf("unexpected REA guidance without an MCP: %q", got)
	}
	if got := binaryAnalysisToolGuidance(DeferredInfo{Deferred: []string{"mcp__rea__binary_overview"}}); got != "" {
		t.Fatalf("skill-gated REA must not be advertised before unlock: %q", got)
	}
	got := binaryAnalysisToolGuidance(DeferredInfo{GlobalNames: []string{"mcp__rea__binary_overview"}})
	for _, want := range []string{"REA", "Capstone", "실제 호출"} {
		if !strings.Contains(got, want) {
			t.Fatalf("REA guidance missing %q: %q", want, got)
		}
	}
}
