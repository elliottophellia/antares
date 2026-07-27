package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/version"
)

const minSidebar = 72 // hide the sidebar below this width

// layout (re)sizes every component from the current window size. Everything is
// clamped so a tiny or huge terminal never breaks the render.
func (m *Model) layout() {
	sidebarW := 26
	if m.width < minSidebar {
		sidebarW = 0
	}
	// Border/padding budget: sidebar has a right border (1); main content sits to
	// its right. Reserve rows for header (2), input (3), and status (1).
	contentW := m.width - sidebarW
	if contentW < 20 {
		contentW = max(m.width, 20)
		sidebarW = 0
	}
	innerW := contentW - 2 // main content horizontal padding
	if innerW < 10 {
		innerW = 10
	}

	headerH, inputH, statusH := 2, 3, 1
	vpH := m.height - headerH - inputH - statusH
	if vpH < 1 {
		vpH = 1
	}

	if m.vp.Width == 0 {
		m.vp = viewport.New(innerW, vpH)
	} else {
		m.vp.Width = innerW
		m.vp.Height = vpH
	}
	m.ta.SetWidth(innerW - 2)

	// Glamour wraps Markdown to the content width.
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle()),
		glamour.WithWordWrap(innerW-2),
	)
	if err == nil {
		m.renderer = r
	}
}

func glamourStyle() string {
	if lipgloss.HasDarkBackground() {
		return "dark"
	}
	return "light"
}

func (m *Model) View() string {
	if !m.ready {
		return "starting antares…"
	}
	sidebarW := 26
	if m.width < minSidebar {
		sidebarW = 0
	}
	main := m.mainColumn()
	if sidebarW == 0 {
		return main
	}
	side := m.st.sidebar.Width(sidebarW - 1).Height(m.height).Render(m.sidebar())
	return lipgloss.JoinHorizontal(lipgloss.Top, side, main)
}

// mainColumn stacks header, transcript, input, and status.
func (m *Model) mainColumn() string {
	pad := lipgloss.NewStyle().Padding(0, 1)

	title := m.title
	if title == "" {
		title = "New conversation"
	}
	head := m.st.header.Render(title)
	if m.sessionID != "" {
		head += "  " + m.st.headerDim.Render("· "+shortID(m.sessionID))
	}
	header := pad.Render(head) + "\n" + pad.Render(m.st.headerDim.Render(strings.Repeat("─", maxi(m.vp.Width, 1))))

	body := pad.Render(m.vp.View())

	input := m.inputView()
	status := pad.Render(m.statusBar())

	col := lipgloss.JoinVertical(lipgloss.Left, header, body, input, status)

	// The command palette floats above the input when active.
	if len(m.palette) > 0 {
		col = lipgloss.JoinVertical(lipgloss.Left, header, body, pad.Render(m.paletteView()), input, status)
	}
	return col
}

func (m *Model) inputView() string {
	box := m.st.inputBox
	if !m.busy {
		box = m.st.inputFocus
	}
	prompt := m.st.userLabel.Render("❯ ")
	field := lipgloss.JoinHorizontal(lipgloss.Top, prompt, m.ta.View())
	return lipgloss.NewStyle().Padding(0, 1).Render(box.Width(m.vp.Width - 2).Render(field))
}

func (m *Model) sidebar() string {
	var b strings.Builder
	b.WriteString(m.st.logo.Render("◆ antares"))
	b.WriteString("\n")
	b.WriteString(m.st.headerDim.Render(version.Version))
	b.WriteString("\n\n")

	section := func(label, value string) {
		b.WriteString(m.st.sideLabel.Render(strings.ToUpper(label)))
		b.WriteString("\n")
		b.WriteString(m.st.sideValue.Render(value))
		b.WriteString("\n\n")
	}
	model, provider := "—", ""
	if m.cfg != nil {
		model, provider = firstNon(m.cfg.Model.Default, "—"), m.cfg.Model.Provider
	}
	section("Model", model)
	if provider != "" {
		section("Provider", provider)
	}
	sess := "new"
	if m.title != "" {
		sess = m.title
	}
	section("Session", truncate(sess, 20))
	section("Tokens", fmt.Sprintf("%d in / %d out", m.tokensIn, m.tokensOut))
	section("Reasoning", onOff(m.showReasoning))

	b.WriteString(m.st.sideLabel.Render("SHORTCUTS"))
	b.WriteString("\n")
	for _, s := range [][2]string{
		{"Enter", "send"}, {"Ctrl+J", "newline"}, {"/", "commands"},
		{"Ctrl+R", "reasoning"}, {"Ctrl+L", "clear"}, {"PgUp/Dn", "scroll"},
		{"Ctrl+C", "stop/quit"},
	} {
		b.WriteString(m.st.statusKey.Render(s[0]))
		b.WriteString(m.st.sideLabel.Render(" " + s[1] + "\n"))
	}
	return b.String()
}

func (m *Model) statusBar() string {
	sep := m.st.statusSep.Render("  ·  ")
	var parts []string
	if m.busy {
		parts = append(parts, m.spin.View()+m.st.status.Render(" working"))
	} else {
		parts = append(parts, m.st.status.Render("ready"))
	}
	if m.status != "" {
		parts = append(parts, m.st.status.Render(m.status))
	}
	if m.tokensOut > 0 {
		parts = append(parts, m.st.status.Render(fmt.Sprintf("%d/%d tok", m.tokensIn, m.tokensOut)))
	}
	if m.vp.ScrollPercent() < 1 && m.vp.TotalLineCount() > m.vp.Height {
		parts = append(parts, m.st.scrollHint.Render(fmt.Sprintf("%d%%↑", int(m.vp.ScrollPercent()*100))))
	}
	line := strings.Join(parts, sep)
	// Right-aligned hint.
	hint := m.st.status.Render("/help")
	gap := m.vp.Width - lipgloss.Width(line) - lipgloss.Width(hint)
	if gap < 1 {
		return truncate(line, maxi(m.vp.Width, 1))
	}
	return line + strings.Repeat(" ", gap) + hint
}

func (m *Model) paletteView() string {
	var b strings.Builder
	for i, c := range m.palette {
		name, desc := m.st.paletteName.Render("/"+c.Name), m.st.paletteDesc.Render(c.Summary)
		row := name + "  " + desc
		if i == m.paletteSel {
			row = m.st.paletteSel.Render("❯ ") + m.st.paletteSel.Render("/"+c.Name) + "  " + desc
		} else {
			row = "  " + row
		}
		b.WriteString(row)
		if i < len(m.palette)-1 {
			b.WriteString("\n")
		}
	}
	return m.st.paletteBox.Width(m.vp.Width - 2).Render(b.String())
}

// refreshTranscript rebuilds the viewport content from the blocks and, unless
// the user has scrolled up, keeps it pinned to the newest line.
func (m *Model) refreshTranscript() {
	if !m.ready {
		return
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(m.renderBlocks())
	if atBottom || m.busy {
		m.vp.GotoBottom()
	}
}

func (m *Model) renderBlocks() string {
	var out []string
	for _, bl := range m.blocks {
		if bl.kind == blockReasoning && !m.showReasoning {
			continue
		}
		out = append(out, m.renderBlock(bl))
	}
	return strings.Join(out, "\n\n")
}

func (m *Model) renderBlock(bl block) string {
	switch bl.kind {
	case blockUser:
		return m.st.userLabel.Render("❯ You") + "\n" + m.st.userText.Render(wrap(bl.text, m.vp.Width))
	case blockAssistant:
		if m.renderer != nil {
			if s, err := m.renderer.Render(bl.text); err == nil {
				return strings.TrimRight(s, "\n")
			}
		}
		return wrap(bl.text, m.vp.Width)
	case blockReasoning:
		return m.st.reasonLabel.Render("reasoning") + "\n" + m.st.reasoning.Render(wrap(bl.text, m.vp.Width-2))
	case blockTool:
		head := m.st.toolLabel.Render("⚙ " + bl.title)
		body := strings.TrimSpace(bl.text)
		if body == "" {
			if bl.streaming {
				return head + "  " + m.st.system.Render("…")
			}
			return head
		}
		style := m.st.toolBox
		if bl.isError {
			style = m.st.errorBox
		}
		return head + "\n" + style.Render(clampLines(body, 12, m.vp.Width-2))
	case blockNotice:
		return m.st.notice.Render("! " + wrap(bl.text, m.vp.Width-2))
	case blockError:
		return m.st.errorBox.Render(wrap(bl.text, m.vp.Width-2))
	case blockSystem:
		return m.st.system.Render(wrap(bl.text, m.vp.Width))
	}
	return bl.text
}

// ---- small helpers ----------------------------------------------------------

func (m *Model) refresh() tea.Cmd { m.refreshTranscript(); return nil }

func wrap(s string, w int) string {
	if w < 4 {
		w = 4
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

func clampLines(s string, maxLines, w int) string {
	lines := strings.Split(s, "\n")
	trimmed := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		trimmed = true
	}
	out := strings.Join(lines, "\n")
	if trimmed {
		out += "\n…"
	}
	return out
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func firstNon(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
