package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/version"
)

// showWelcome reports whether the animated splash should fill the viewport. It
// does so only while the transcript is completely empty — any block at all
// (including a system reply from a command like /help) replaces it, so command
// output is never hidden behind the splash.
func (m *Model) showWelcome() bool {
	return len(m.blocks) == 0
}

// welcomeView renders the empty-state splash: the antares logo (braille), the
// ANTARES wordmark, a tagline, and a few hints — coloured with the theme accent
// and animated by a shimmer that sweeps across the wordmark each frame.
func (m *Model) welcomeView(w, h int) string {
	t := themeByName(m.themeName)

	var logo, banner string
	big := w >= 60
	switch {
	case big:
		logo, banner = logoArtLarge, bannerLarge
	case w >= 38:
		logo, banner = logoArtSmall, bannerSmall
	default:
		// Too narrow for art — a simple centred mark.
		mark := lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render("◆ ANTARES")
		tag := lipgloss.NewStyle().Foreground(t.Faint).Render(version.Version)
		body := lipgloss.JoinVertical(lipgloss.Center, mark, "", tag)
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, body)
	}

	logoBlock := lipgloss.NewStyle().Foreground(t.Accent).Render(logo)
	bannerBlock := m.shimmer(banner, t)

	tagline := lipgloss.NewStyle().Foreground(t.Muted).Render("self-hosted AI agent") +
		lipgloss.NewStyle().Foreground(t.Faint).Render("  ·  "+version.Version)

	dot := lipgloss.NewStyle().Foreground(t.Accent).Render(" · ")
	hint := func(k, v string) string {
		return lipgloss.NewStyle().Foreground(t.Muted).Bold(true).Render(k) + " " +
			lipgloss.NewStyle().Foreground(t.Faint).Render(v)
	}
	hints := hint("Enter", "send") + dot + hint("/", "commands") + dot +
		hint("Ctrl+R", "reasoning") + dot + hint("Ctrl+C", "quit")
	if !big {
		hints = hint("/", "commands") + dot + hint("Ctrl+C", "quit")
	}

	prompt := lipgloss.NewStyle().Foreground(t.Faint).Italic(true).
		Render("Type a message to begin, or /help to explore.")

	parts := []string{logoBlock, "", bannerBlock, "", tagline, "", prompt, "", hints}
	if m.cfg != nil && m.cfg.Model.Default == "" && !m.demo {
		notice := lipgloss.NewStyle().Foreground(t.Yellow).
			Render("⚠ No model selected — run /setup, or set model.default in the dashboard.")
		parts = append(parts, "", notice)
	}
	body := lipgloss.JoinVertical(lipgloss.Center, parts...)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, body)
}

// shimmer colours the wordmark with a moving highlight band: each glyph column
// is tinted between the muted base and a bright accent, and the bright centre
// slides one column per frame — a gentle sweep of light across ANTARES.
func (m *Model) shimmer(art string, t Theme) string {
	dark := lipgloss.HasDarkBackground()
	glow := hexToRGB(hexFor(t.Accent, dark))
	// A dim, warm version of the accent so the whole wordmark reads as coloured
	// even away from the moving highlight (rather than a flat grey).
	base := lerpRGB(glow, rgb{34, 38, 46}, 0.6)
	bright := hexToRGB("#FFFFFF")

	lines := strings.Split(art, "\n")
	width := 0
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > width {
			width = w
		}
	}
	if width == 0 {
		return art
	}

	// The highlight centre moves across a span a bit wider than the wordmark so
	// the sweep glides fully off one edge before returning.
	span := width + 16
	centre := m.welcomeFrame % span

	colFor := func(x int) string {
		d := x - centre
		if d < 0 {
			d = -d
		}
		switch {
		case d <= 1: // bright core
			return rgbHex(lerpRGB(glow, bright, 0.65))
		case d <= 5: // accent glow, fading with distance
			f := 1 - float64(d-1)/5
			return rgbHex(lerpRGB(base, glow, f))
		default:
			return rgbHex(base)
		}
	}

	var out strings.Builder
	for li, ln := range lines {
		if li > 0 {
			out.WriteByte('\n')
		}
		col := 0
		for _, r := range ln {
			if r == ' ' {
				out.WriteByte(' ')
				col++
				continue
			}
			out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(colFor(col))).Render(string(r)))
			col++
		}
	}
	return out.String()
}

// ---- tiny colour helpers ----------------------------------------------------

type rgb struct{ r, g, b int }

func hexToRGB(h string) rgb {
	h = strings.TrimPrefix(h, "#")
	if len(h) != 6 {
		return rgb{200, 200, 200}
	}
	v, _ := strconv.ParseInt(h, 16, 64)
	return rgb{int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff}
}

func rgbHex(c rgb) string {
	clamp := func(x int) int {
		if x < 0 {
			return 0
		}
		if x > 255 {
			return 255
		}
		return x
	}
	const hexdig = "0123456789abcdef"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	vals := []int{clamp(c.r), clamp(c.g), clamp(c.b)}
	for i, v := range vals {
		b[1+i*2] = hexdig[v>>4]
		b[2+i*2] = hexdig[v&0xf]
	}
	return string(b)
}

func lerpRGB(a, b rgb, f float64) rgb {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return rgb{
		int(float64(a.r) + (float64(b.r)-float64(a.r))*f),
		int(float64(a.g) + (float64(b.g)-float64(a.g))*f),
		int(float64(a.b) + (float64(b.b)-float64(a.b))*f),
	}
}
