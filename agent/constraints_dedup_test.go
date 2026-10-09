package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRenderConstraintRowsDeduplicatesExistingRecords(t *testing.T) {
	got := renderConstraintRows([]db.Constraint{
		{Kind: "deny", Text: " 외부 호스트 접근 금지 "},
		{Kind: "deny", Text: "외부 호스트 접근 금지"},
		{Kind: "allow", Text: "127.0.0.1 허용"},
		{Kind: "allow", Text: "127.0.0.1 허용"},
	})
	if strings.Count(got, "외부 호스트 접근 금지") != 1 || strings.Count(got, "127.0.0.1 허용") != 1 {
		t.Fatalf("duplicate constraints in prompt: %s", got)
	}
}
