package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/config"
)

// Tool output is handed straight to the provider, so a cut that lands inside a
// rune ships invalid UTF-8 that JSON encoding rewrites as U+FFFD. Terminal and
// browser output is where multi-byte text actually turns up.
func TestTrimOutputKeepsValidUTF8AndCountsRunes(t *testing.T) {
	got := trimOutput(strings.Repeat("é", 100), 51)
	if !utf8.ValidString(got) {
		t.Fatalf("trimOutput produced invalid UTF-8: %q", got)
	}
	want := strings.Repeat("é", 34) + "\n\n… 49 characters omitted …\n\n" + strings.Repeat("é", 17)
	if got != want {
		t.Fatalf("trimOutput = %q, want %q", got, want)
	}
}

// The notice must state characters removed, not bytes removed: 100 runes cut to
// 51 loses 49 characters, whatever each one weighs.
func TestTrimOutputReportsCharactersNotBytes(t *testing.T) {
	got := trimOutput(strings.Repeat("字", 100), 51)
	if !strings.Contains(got, "… 49 characters omitted …") {
		t.Fatalf("trimOutput notice does not report 49 characters removed: %q", got)
	}
}

// MaxOutputChars is a character budget, so text inside it survives whole no
// matter how many bytes each character takes.
func TestTrimOutputDefaultBudgetCountsCharacters(t *testing.T) {
	in := strings.Repeat("字", 30000) // 90000 bytes, inside the 60000-character default
	if got := trimOutput(in, 0); got != in {
		t.Fatalf("trimOutput cut a %d-character string under the 60000-character default (%d bytes kept)",
			utf8.RuneCountInString(in), len(got))
	}
}

// truncateTool caps browser page text, diagnostics, image-endpoint errors and
// the schedule listing. Its notice reports a total, so that total must count
// characters too.
func TestTruncateToolKeepsValidUTF8AndCountsRunes(t *testing.T) {
	got := truncateTool(strings.Repeat("字", 200), 100)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateTool produced invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("字", 100)) {
		t.Fatalf("truncateTool kept %d characters, want 100: %q", utf8.RuneCountInString(got), got)
	}
	if !strings.Contains(got, "… truncated, 200 characters total") {
		t.Fatalf("truncateTool notice does not report 200 characters total: %q", got)
	}
}

// The hook tools cap their body with truncateTool at Tools.MaxOutputChars
// (hooks.go), defaulting to 60000. The hook path itself shells out to an
// embedded Python program, so the budget is covered here rather than by running
// one: what matters is that the cap holds characters, not bytes.
func TestHookOutputBudgetCountsCharacters(t *testing.T) {
	in := strings.Repeat("字", 30000) // 90000 bytes, inside the 60000-character default
	if got := truncateTool(in, 60000); got != in {
		t.Fatalf("truncateTool cut a %d-character body under a 60000-character budget (%d bytes kept)",
			utf8.RuneCountInString(in), len(got))
	}
}

// End to end through the terminal tool, the call site that actually feeds the
// model: a command whose output exceeds MaxOutputChars must still come back as
// valid UTF-8.
func TestTerminalToolOutputStaysValidUTF8(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent shell protocol does not apply on Windows")
	}

	cfg := &config.Config{
		Terminal: config.Terminal{Sandbox: "none"},
		Tools:    config.Tools{MaxOutputChars: 51},
	}
	m := NewShellManager(cfg.Terminal)
	t.Cleanup(m.CloseAll)

	args, _ := json.Marshal(map[string]any{"command": "printf '%s' '" + strings.Repeat("é", 100) + "'"})
	res := (terminalTool{}).Execute(context.Background(), Input{
		Args: args, SessionID: "session-utf8", Workspace: t.TempDir(),
		Deps: &Deps{Shell: m, Config: cfg}, Emit: func(Progress) {},
	})
	if res.IsError {
		t.Fatalf("terminal tool failed: %s", res.Content)
	}
	if !utf8.ValidString(res.Content) {
		t.Fatalf("terminal output is not valid UTF-8: %q", res.Content)
	}
	want := strings.Repeat("é", 34) + "\n\n… 49 characters omitted …\n\n" + strings.Repeat("é", 17)
	if res.Content != want {
		t.Fatalf("terminal output = %q, want %q", res.Content, want)
	}
}
