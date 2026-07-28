package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestWelcomeRender(t *testing.T) {
	m := &Model{themeName: "antares", welcomeFrame: 7}
	m.vp = viewport.New(96, 26)
	out := m.welcomeView(96, 26)
	// strip ANSI to inspect structure
	plain := stripANSITest(out)
	t.Logf("\n%s", plain)
	lines := strings.Split(out, "\n")
	if len(lines) != 26 {
		t.Fatalf("want 26 rows, got %d", len(lines))
	}
	for i, ln := range strings.Split(plain, "\n") {
		if w := len([]rune(ln)); w > 96 {
			t.Fatalf("row %d width %d exceeds 96", i, w)
		}
	}
}

// TestCommandOutputShowsOnEmptyTranscript guards the bug where a command's
// system reply (e.g. /help) on an empty chat was hidden behind the welcome splash.
func TestCommandOutputShowsOnEmptyTranscript(t *testing.T) {
	m := &Model{themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 20)
	if !m.showWelcome() {
		t.Fatal("empty transcript should show the welcome")
	}
	m.pushSystem("hello from /help")
	if m.showWelcome() {
		t.Fatal("a pushed system block must replace the welcome")
	}
	out := stripANSITest(m.renderBlocks())
	if !strings.Contains(out, "hello from /help") {
		t.Fatalf("system block not rendered: %q", out)
	}
	if strings.Contains(out, "ANTARES") {
		t.Fatal("welcome banner still shown behind command output")
	}
}

func stripANSITest(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
