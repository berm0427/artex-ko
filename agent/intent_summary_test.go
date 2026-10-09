package agent

import "testing"

func TestNormalizedIntentSummary(t *testing.T) {
	a := normalizedIntentSummary("  Check   Alice\ninvoice  ")
	b := normalizedIntentSummary("check alice invoice")
	if a != b {
		t.Fatalf("equivalent active intents differ: %q != %q", a, b)
	}
}
