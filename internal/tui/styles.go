package tui

import "github.com/charmbracelet/lipgloss"

// theme holds the colour palette. Colours are adaptive so the UI stays legible
// on both light and dark terminals, and degrade gracefully on 16-colour ones.
var (
	colPrimary = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#A78BFA"}
	colAccent  = lipgloss.AdaptiveColor{Light: "#DB2777", Dark: "#F472B6"}
	colText    = lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#E5E7EB"}
	colMuted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	colFaint   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"}
	colBorder  = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#374151"}
	colGreen   = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"}
	colRed     = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"}
	colYellow  = lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#FBBF24"}
	colBlue    = lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"}
)

type styles struct {
	sidebar     lipgloss.Style
	logo        lipgloss.Style
	navActive   lipgloss.Style
	navItem     lipgloss.Style
	sideLabel   lipgloss.Style
	sideValue   lipgloss.Style
	header      lipgloss.Style
	headerDim   lipgloss.Style
	userLabel   lipgloss.Style
	userText    lipgloss.Style
	reasoning   lipgloss.Style
	reasonLabel lipgloss.Style
	toolLabel   lipgloss.Style
	toolBox     lipgloss.Style
	notice      lipgloss.Style
	errorBox    lipgloss.Style
	system      lipgloss.Style
	inputBox    lipgloss.Style
	inputFocus  lipgloss.Style
	status      lipgloss.Style
	statusKey   lipgloss.Style
	statusSep   lipgloss.Style
	paletteBox  lipgloss.Style
	paletteSel  lipgloss.Style
	paletteName lipgloss.Style
	paletteDesc lipgloss.Style
	scrollHint  lipgloss.Style
}

func newStyles() styles {
	return styles{
		sidebar:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), false, true, false, false).BorderForeground(colBorder).Padding(1, 2),
		logo:        lipgloss.NewStyle().Foreground(colPrimary).Bold(true),
		navActive:   lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		navItem:     lipgloss.NewStyle().Foreground(colMuted),
		sideLabel:   lipgloss.NewStyle().Foreground(colFaint),
		sideValue:   lipgloss.NewStyle().Foreground(colText),
		header:      lipgloss.NewStyle().Foreground(colText).Bold(true),
		headerDim:   lipgloss.NewStyle().Foreground(colMuted),
		userLabel:   lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		userText:    lipgloss.NewStyle().Foreground(colText),
		reasoning:   lipgloss.NewStyle().Foreground(colFaint).Italic(true),
		reasonLabel: lipgloss.NewStyle().Foreground(colMuted).Italic(true),
		toolLabel:   lipgloss.NewStyle().Foreground(colBlue).Bold(true),
		toolBox:     lipgloss.NewStyle().Foreground(colMuted).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colBorder).PaddingLeft(1),
		notice:      lipgloss.NewStyle().Foreground(colYellow),
		errorBox:    lipgloss.NewStyle().Foreground(colRed).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colRed).PaddingLeft(1),
		system:      lipgloss.NewStyle().Foreground(colFaint),
		inputBox:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1),
		inputFocus:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPrimary).Padding(0, 1),
		status:      lipgloss.NewStyle().Foreground(colMuted),
		statusKey:   lipgloss.NewStyle().Foreground(colText).Bold(true),
		statusSep:   lipgloss.NewStyle().Foreground(colFaint),
		paletteBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPrimary).Padding(0, 1),
		paletteSel:  lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		paletteName: lipgloss.NewStyle().Foreground(colText),
		paletteDesc: lipgloss.NewStyle().Foreground(colFaint),
		scrollHint:  lipgloss.NewStyle().Foreground(colFaint),
	}
}
