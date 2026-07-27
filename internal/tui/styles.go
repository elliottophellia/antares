package tui

import "github.com/charmbracelet/lipgloss"

// Palette — a Charm-flavoured scheme (purple/pink/blue) that adapts to light and
// dark terminals and degrades on 16-colour ones.
var (
	colPrimary = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#B794F6"} // assistant / brand
	colAccent  = lipgloss.AdaptiveColor{Light: "#DB2777", Dark: "#F472B6"} // user
	colBlue    = lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#63B3ED"} // tools
	colText    = lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#E2E8F0"}
	colMuted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#94A3B8"}
	colFaint   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#64748B"}
	colBorder  = lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#2D3748"}
	colGreen   = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#48BB78"}
	colRed     = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#FC8181"}
	colYellow  = lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#F6AD55"}
	colOnBadge = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0B1020"} // text on a coloured badge
	colPanel   = lipgloss.AdaptiveColor{Light: "#FAFAFA", Dark: "#161B26"} // subtle panel fill
)

type styles struct {
	// sidebar
	sidebar   lipgloss.Style
	logo      lipgloss.Style
	sideLabel lipgloss.Style
	sideValue lipgloss.Style
	dotOn     lipgloss.Style
	pillOn    lipgloss.Style

	// header
	header    lipgloss.Style
	headerDim lipgloss.Style
	tokenPill lipgloss.Style
	rule      lipgloss.Style

	// message badges
	userBadge   lipgloss.Style
	asstBadge   lipgloss.Style
	toolBadge   lipgloss.Style
	reasonBadge lipgloss.Style

	// message cards / bodies
	userCard   lipgloss.Style
	userText   lipgloss.Style
	asstBar    lipgloss.Style
	toolCard   lipgloss.Style
	toolMeta   lipgloss.Style
	toolResult lipgloss.Style
	reasoning  lipgloss.Style
	notice     lipgloss.Style
	errorCard  lipgloss.Style
	system     lipgloss.Style

	// statuses
	stRunning lipgloss.Style
	stDone    lipgloss.Style
	stErr     lipgloss.Style

	// input + status bar
	inputBox   lipgloss.Style
	inputFocus lipgloss.Style
	prompt     lipgloss.Style
	status     lipgloss.Style
	statusKey  lipgloss.Style
	statusSep  lipgloss.Style
	scrollHint lipgloss.Style

	// palette
	paletteBox  lipgloss.Style
	paletteSel  lipgloss.Style
	paletteName lipgloss.Style
	paletteDesc lipgloss.Style
}

func newStyles() styles {
	badge := func(c lipgloss.TerminalColor) lipgloss.Style {
		return lipgloss.NewStyle().Background(c).Foreground(colOnBadge).Bold(true).Padding(0, 1)
	}
	leftBar := func(c lipgloss.TerminalColor) lipgloss.Style {
		return lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(c).PaddingLeft(1)
	}
	return styles{
		sidebar:   lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, false).BorderForeground(colBorder).Padding(1, 2),
		logo:      lipgloss.NewStyle().Foreground(colPrimary).Bold(true),
		sideLabel: lipgloss.NewStyle().Foreground(colFaint).Bold(true),
		sideValue: lipgloss.NewStyle().Foreground(colText),
		dotOn:     lipgloss.NewStyle().Foreground(colGreen),
		pillOn:    lipgloss.NewStyle().Foreground(colGreen),

		header:    lipgloss.NewStyle().Foreground(colText).Bold(true),
		headerDim: lipgloss.NewStyle().Foreground(colMuted),
		tokenPill: lipgloss.NewStyle().Foreground(colMuted).Background(colPanel).Padding(0, 1),
		rule:      lipgloss.NewStyle().Foreground(colBorder),

		userBadge:   badge(colAccent),
		asstBadge:   badge(colPrimary),
		toolBadge:   badge(colBlue),
		reasonBadge: lipgloss.NewStyle().Foreground(colFaint).Italic(true),

		userCard:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1),
		userText:   lipgloss.NewStyle().Foreground(colText),
		asstBar:    leftBar(colPrimary),
		toolCard:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBlue).Padding(0, 1),
		toolMeta:   lipgloss.NewStyle().Foreground(colBlue).Bold(true),
		toolResult: lipgloss.NewStyle().Foreground(colMuted),
		reasoning:  lipgloss.NewStyle().Foreground(colFaint).Italic(true),
		notice:     lipgloss.NewStyle().Foreground(colYellow).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colYellow).PaddingLeft(1),
		errorCard:  lipgloss.NewStyle().Foreground(colRed).Border(lipgloss.RoundedBorder()).BorderForeground(colRed).Padding(0, 1),
		system:     lipgloss.NewStyle().Foreground(colFaint),

		stRunning: lipgloss.NewStyle().Foreground(colYellow),
		stDone:    lipgloss.NewStyle().Foreground(colGreen),
		stErr:     lipgloss.NewStyle().Foreground(colRed),

		inputBox:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1),
		inputFocus: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPrimary).Padding(0, 1),
		prompt:     lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		status:     lipgloss.NewStyle().Foreground(colMuted),
		statusKey:  lipgloss.NewStyle().Foreground(colText).Bold(true),
		statusSep:  lipgloss.NewStyle().Foreground(colFaint),
		scrollHint: lipgloss.NewStyle().Foreground(colPrimary),

		paletteBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPrimary).Padding(0, 1),
		paletteSel:  lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		paletteName: lipgloss.NewStyle().Foreground(colText).Bold(true),
		paletteDesc: lipgloss.NewStyle().Foreground(colFaint),
	}
}
