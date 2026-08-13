package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// grep caps every line it prints, and the cap landed on a byte offset. Nothing
// makes a source line stop at an ASCII boundary — a comment, a string literal,
// a translation table — and grep is in every toolset including minimal, so this
// is the widest path from a repository into a provider request. A cut inside a
// rune ships bytes that are not UTF-8, which JSON encoding rewrites as U+FFFD.
func TestGrepKeepsValidUTF8WhenALineIsCapped(t *testing.T) {
	ws := t.TempDir()
	// Three bytes per rune, so the three offsets a byte cap can land on are all
	// exercised: one lands on a boundary and two land inside a rune.
	for i, pad := range []string{"", "x", "xx"} {
		line := pad + strings.Repeat("字", 300) + " needle"
		if err := os.WriteFile(filepath.Join(ws, "cjk"+string(rune('a'+i))+".go"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	args, _ := json.Marshal(map[string]any{"pattern": "字", "path": "."})
	res := (grepTool{}).Execute(context.Background(), Input{Workspace: ws, Args: args})
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	if !utf8.ValidString(res.Content) {
		t.Fatalf("grep returned invalid UTF-8: %q", res.Content)
	}
}

// The cap is a character budget, so text within it survives whole however many
// bytes each character takes. At 400 bytes a 300-character line lost two thirds
// of itself while the tool reported nothing missing.
func TestGrepDoesNotCutALineInsideTheBudget(t *testing.T) {
	line := strings.Repeat("字", 300) // 900 bytes, 300 characters
	if got := truncateLine(line); got != line {
		t.Fatalf("a %d-character line was cut to %d characters by a 400-character cap",
			utf8.RuneCountInString(line), utf8.RuneCountInString(got))
	}
}

// A line genuinely past the budget is still cut, on a rune boundary, to the
// number of characters the budget names.
func TestGrepCutsAnOverlongLineOnARuneBoundary(t *testing.T) {
	got := truncateLine(strings.Repeat("字", 500))
	if !utf8.ValidString(got) {
		t.Fatalf("truncateLine produced invalid UTF-8: %q", got)
	}
	want := strings.Repeat("字", 400) + "…"
	if got != want {
		t.Fatalf("truncateLine kept %d characters, want 400 and an ellipsis",
			utf8.RuneCountInString(strings.TrimSuffix(got, "…")))
	}
}
