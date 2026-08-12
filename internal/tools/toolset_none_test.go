package tools

import "testing"

// The "none" toolset must yield zero tools — not fall back to "default", and
// not pick up the otherwise opt-out MCP tools.
func TestToolsetNoneIsEmpty(t *testing.T) {
	if got := ExpandToolset("none"); len(got) != 0 {
		t.Fatalf("ExpandToolset(none) = %v, want empty", got)
	}
	// Resolve against the populated global registry so "none → 0" is a real
	// assertion, not a vacuous one against an empty registry. Sanity-check that
	// the registry is non-empty first.
	r := Default()
	if len(r.Resolve("default", nil, nil)) == 0 {
		t.Skip("global registry empty in this build; nothing to prove against")
	}
	if got := r.Resolve("none", nil, nil); len(got) != 0 {
		t.Fatalf("Resolve(none) returned %d tools, want 0", len(got))
	}
	// Even an enable list does not override an explicit "none".
	if got := r.Resolve("none", []string{"read_file"}, nil); len(got) != 0 {
		t.Fatalf("Resolve(none, enable=read_file) returned %d tools, want 0", len(got))
	}
	// "none" is listed so the UI can offer it.
	found := false
	for _, n := range ToolsetNames() {
		if n == "none" {
			found = true
		}
	}
	if !found {
		t.Fatal("ToolsetNames() must include none")
	}
}
