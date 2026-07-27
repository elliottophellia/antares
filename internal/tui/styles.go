package tui

import "github.com/charmbracelet/lipgloss"

// styles are built from a Theme. The look is near-monochrome greys; the theme's
// accent is the only highlight (prompt, focus, the "you" marker), and ✓/✗ carry
// the sole green/red. This keeps the UI calm rather than colourful.
type styles struct {
	// sidebar
	sidebar   lipgloss.Style
	logo      lipgloss.Style
	sideLabel lipgloss.Style
	sideValue lipgloss.Style

	// header
	header    lipgloss.Style
	headerDim lipgloss.Style
	rule      lipgloss.Style

	// role labels + subtle rules
	userLabel  lipgloss.Style
	userBar    lipgloss.Style
	userText   lipgloss.Style
	asstLabel  lipgloss.Style
	reasonLbl  lipgloss.Style
	reasonBar  lipgloss.Style
	reasoning  lipgloss.Style
	toolName   lipgloss.Style
	toolArgs   lipgloss.Style
	toolBar    lipgloss.Style
	toolResult lipgloss.Style
	errLabel   lipgloss.Style
	errBar     lipgloss.Style
	errText    lipgloss.Style
	notice     lipgloss.Style
	system     lipgloss.Style

	// statuses
	stRunning lipgloss.Style
	stDone    lipgloss.Style
	stErr     lipgloss.Style

	// input + status
	inputBox   lipgloss.Style
	inputFocus lipgloss.Style
	prompt     lipgloss.Style
	status     lipgloss.Style
	statusKey  lipgloss.Style
	statusSep  lipgloss.Style
	accent     lipgloss.Style

	// palette
	paletteBox  lipgloss.Style
	paletteSel  lipgloss.Style
	paletteName lipgloss.Style
	paletteDesc lipgloss.Style
}

func newStyles(t Theme) styles {
	leftBar := func(col lipgloss.TerminalColor) lipgloss.Style {
		return lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(col).PaddingLeft(1)
	}
	return styles{
		sidebar:   lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, false).BorderForeground(t.Border).Padding(1, 2),
		logo:      lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		sideLabel: lipgloss.NewStyle().Foreground(t.Faint),
		sideValue: lipgloss.NewStyle().Foreground(t.Text),

		header:    lipgloss.NewStyle().Foreground(t.Text).Bold(true),
		headerDim: lipgloss.NewStyle().Foreground(t.Faint),
		rule:      lipgloss.NewStyle().Foreground(t.Border),

		userLabel:  lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		userBar:    leftBar(t.Accent),
		userText:   lipgloss.NewStyle().Foreground(t.Text),
		asstLabel:  lipgloss.NewStyle().Foreground(t.Muted).Bold(true),
		reasonLbl:  lipgloss.NewStyle().Foreground(t.Faint).Italic(true),
		reasonBar:  leftBar(t.Border),
		reasoning:  lipgloss.NewStyle().Foreground(t.Faint).Italic(true),
		toolName:   lipgloss.NewStyle().Foreground(t.Text),
		toolArgs:   lipgloss.NewStyle().Foreground(t.Faint),
		toolBar:    leftBar(t.Border),
		toolResult: lipgloss.NewStyle().Foreground(t.Faint),
		errLabel:   lipgloss.NewStyle().Foreground(t.Red).Bold(true),
		errBar:     leftBar(t.Red),
		errText:    lipgloss.NewStyle().Foreground(t.Red),
		notice:     lipgloss.NewStyle().Foreground(t.Yellow),
		system:     lipgloss.NewStyle().Foreground(t.Faint),

		stRunning: lipgloss.NewStyle().Foreground(t.Yellow),
		stDone:    lipgloss.NewStyle().Foreground(t.Green),
		stErr:     lipgloss.NewStyle().Foreground(t.Red),

		inputBox:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Border).Padding(0, 1),
		inputFocus: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Accent).Padding(0, 1),
		prompt:     lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		status:     lipgloss.NewStyle().Foreground(t.Muted),
		statusKey:  lipgloss.NewStyle().Foreground(t.Muted).Bold(true),
		statusSep:  lipgloss.NewStyle().Foreground(t.Faint),
		accent:     lipgloss.NewStyle().Foreground(t.Accent),

		paletteBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Border).Padding(0, 1),
		paletteSel:  lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		paletteName: lipgloss.NewStyle().Foreground(t.Text),
		paletteDesc: lipgloss.NewStyle().Foreground(t.Faint),
	}
}
