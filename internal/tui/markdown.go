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

	// Headings in the accent, no filled background, and — crucially — no literal
	// "## "/"### " prefixes (Glamour's default), which otherwise leak into the
	// output and look like unrendered markdown.
	heading := func(h *ansi.StyleBlock, bold bool) {
		h.Prefix = ""
		h.Suffix = ""
		h.Color = sp(accent)
		h.BackgroundColor = nil
		h.Bold = sp2(bold)
	}
	cfg.Heading.Prefix = ""
	cfg.Heading.Suffix = ""
	cfg.Heading.Color = sp(accent)
	cfg.Heading.BackgroundColor = nil
	heading(&cfg.H1, true)
	heading(&cfg.H2, true)
	heading(&cfg.H3, true)
	heading(&cfg.H4, false)
	heading(&cfg.H5, false)
	heading(&cfg.H6, false)

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
