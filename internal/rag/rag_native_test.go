package rag

import (
	"testing"

	"github.com/enowdev/antares/internal/tools"
)

func TestDedupeCollapsesIdenticalContent(t *testing.T) {
	in := []tools.RAGResult{
		{Content: "alpha", Path: "a"},
		{Content: "beta", Path: "b"},
		{Content: "alpha", Path: "c"},  // duplicate of the first
		{Content: " beta ", Path: "d"}, // whitespace-only difference → dup of beta
	}
	out := dedupe(in)
	if len(out) != 2 {
		t.Fatalf("want 2 after dedupe, got %d: %+v", len(out), out)
	}
	// First occurrence wins and order is preserved.
	if out[0].Path != "a" || out[1].Path != "b" {
		t.Fatalf("wrong survivors/order: %+v", out)
	}
}

func TestParseRerankJSON(t *testing.T) {
	// Tolerates surrounding prose and a code fence; sorts by score desc; drops
	// out-of-range and duplicate indices.
	reply := "Sure! ```json\n[{\"i\":2,\"s\":9},{\"i\":0,\"s\":3},{\"i\":9,\"s\":10},{\"i\":2,\"s\":1}]\n``` done"
	items := parseRerankJSON(reply, 3)
	if len(items) != 2 {
		t.Fatalf("want 2 valid items (0..2, deduped), got %d: %+v", len(items), items)
	}
	if items[0].i != 2 || items[1].i != 0 {
		t.Fatalf("want order [2,0] by score, got [%d,%d]", items[0].i, items[1].i)
	}
}

func TestParseRerankJSONRejectsGarbage(t *testing.T) {
	if got := parseRerankJSON("not json at all", 5); got != nil {
		t.Fatalf("expected nil for non-JSON, got %+v", got)
	}
}
