package tui

import (
	"github.com/charmbracelet/glamour/ansi"
	gstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

func hexFor(a lipgloss.AdaptiveColor, dark bool) string {
	if dark {
		return a.Dark
	}
	return a.Light
}

func sp(s string) *string { return &s }

// glamourStyle tightens the default Glamour style and tints it with the active
// theme: headings, list bullets, links, and inline code take the accent; inline
// code loses its heavy background/padding; block margins are trimmed so the
// transcript reads compactly.
func glamourStyle(t Theme, dark bool) ansi.StyleConfig {
	cfg := gstyles.DarkStyleConfig
	if !dark {
		cfg = gstyles.LightStyleConfig
	}
	accent := hexFor(t.Accent, dark)
	text := hexFor(t.Text, dark)
	faint := hexFor(t.Faint, dark)
	var zero uint

	cfg.Document.Margin = &zero
	cfg.Document.Color = sp(text)

	// Inline code: a clean accent-coloured span, no background or padding.
	cfg.Code.Color = sp(accent)
	cfg.Code.BackgroundColor = nil
	cfg.Code.Prefix = ""
	cfg.Code.Suffix = ""

	// Headings in the accent, no filled background.
	cfg.Heading.Color = sp(accent)
	cfg.Heading.BackgroundColor = nil
	cfg.H1.Color = sp(accent)
	cfg.H1.BackgroundColor = nil
	cfg.H1.Bold = sp2(true)
	cfg.H2.Color = sp(accent)
	cfg.H3.Color = sp(accent)

	// List bullets / numbers in the accent.
	cfg.Item.Color = sp(accent)
	cfg.Enumeration.Color = sp(accent)

	// Links + quotes.
	cfg.Link.Color = sp(accent)
	cfg.LinkText.Color = sp(accent)
	cfg.BlockQuote.Color = sp(faint)

	return cfg
}

func sp2(b bool) *bool { return &b }
