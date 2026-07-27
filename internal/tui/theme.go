package tui

import (
	"sort"

	"github.com/charmbracelet/lipgloss"
)

// Theme is a named colour scheme. Everything else in the UI is greyscale; a
// theme only picks the accent and the neutral ramp, so switching stays calm.
type Theme struct {
	Name   string
	Accent lipgloss.AdaptiveColor // the single highlight (prompt, focus, "you")
	Text   lipgloss.AdaptiveColor
	Muted  lipgloss.AdaptiveColor
	Faint  lipgloss.AdaptiveColor
	Border lipgloss.AdaptiveColor
	Green  lipgloss.AdaptiveColor
	Red    lipgloss.AdaptiveColor
	Yellow lipgloss.AdaptiveColor
}

func c(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// themes: the default is a grey/black scheme (like OpenCode) with a restrained
// warm-orange accent. The rest change only the accent + neutral temperature.
var themes = map[string]Theme{
	"opencode": {
		Name:   "opencode",
		Accent: c("#C2410C", "#FAB283"),
		Text:   c("#24283B", "#CBD2E0"), Muted: c("#5C6370", "#8B93A7"), Faint: c("#9CA3AF", "#5A6172"),
		Border: c("#E4E4E7", "#2A2E3A"), Green: c("#3F7A3F", "#9ECE6A"), Red: c("#B4413A", "#F7768E"), Yellow: c("#B45309", "#E0AF68"),
	},
	"mono": {
		Name:   "mono",
		Accent: c("#111111", "#E6E6E6"),
		Text:   c("#1F1F1F", "#D0D0D0"), Muted: c("#6B7280", "#8A8A8A"), Faint: c("#A1A1AA", "#5A5A5A"),
		Border: c("#E4E4E7", "#2C2C2C"), Green: c("#4B5563", "#B8B8B8"), Red: c("#B4413A", "#F0A0A0"), Yellow: c("#6B7280", "#C8C8C8"),
	},
	"tokyonight": {
		Name:   "tokyonight",
		Accent: c("#2E7DE9", "#7AA2F7"),
		Text:   c("#343B58", "#C0CAF5"), Muted: c("#6172B0", "#9AA5CE"), Faint: c("#9BA3C9", "#565F89"),
		Border: c("#E1E2E7", "#292E42"), Green: c("#587539", "#9ECE6A"), Red: c("#8C4351", "#F7768E"), Yellow: c("#8F5E15", "#E0AF68"),
	},
	"dracula": {
		Name:   "dracula",
		Accent: c("#7C3AED", "#BD93F9"),
		Text:   c("#282A36", "#F8F8F2"), Muted: c("#6272A4", "#A9B1D6"), Faint: c("#9AA0C0", "#6272A4"),
		Border: c("#E4E4E7", "#343746"), Green: c("#2E7D32", "#50FA7B"), Red: c("#C62828", "#FF5555"), Yellow: c("#B45309", "#F1FA8C"),
	},
	"gruvbox": {
		Name:   "gruvbox",
		Accent: c("#AF3A03", "#FE8019"),
		Text:   c("#3C3836", "#EBDBB2"), Muted: c("#7C6F64", "#A89984"), Faint: c("#A89984", "#665C54"),
		Border: c("#EBDBB2", "#3C3836"), Green: c("#79740E", "#B8BB26"), Red: c("#9D0006", "#FB4934"), Yellow: c("#B57614", "#FABD2F"),
	},
	"forest": {
		Name:   "forest",
		Accent: c("#0F766E", "#5EEAD4"),
		Text:   c("#1F2937", "#D1FAE5"), Muted: c("#4B7C6F", "#7FB3A3"), Faint: c("#94A3A0", "#4B6358"),
		Border: c("#E4E7E7", "#243330"), Green: c("#3F7A3F", "#6EE7B7"), Red: c("#B4413A", "#FCA5A5"), Yellow: c("#B45309", "#FCD34D"),
	},
}

const defaultTheme = "opencode"

func themeByName(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes[defaultTheme]
}

func themeNames() []string {
	out := make([]string, 0, len(themes))
	for n := range themes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
