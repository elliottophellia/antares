package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/version"
)

const minSidebar = 74 // hide the sidebar below this width

// layout (re)sizes every component from the current window size. Everything is
// clamped so a tiny or huge terminal never breaks the render.
func (m *Model) layout() {
	sidebarW := m.sidebarWidth()
	contentW := m.width - sidebarW
	if contentW < 24 {
		contentW = maxi(m.width, 24)
	}
	innerW := contentW - 2 // main column horizontal padding
	if innerW < 12 {
		innerW = 12
	}

	headerH, inputH, statusH := 2, 3, 1
	vpH := m.height - headerH - inputH - statusH
	if vpH < 1 {
		vpH = 1
	}

	if m.vp.Width == 0 {
		m.vp = viewport.New(innerW, vpH)
	} else {
		m.vp.Width, m.vp.Height = innerW, vpH
	}
	m.ta.SetWidth(innerW - 3)

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle()),
		glamour.WithWordWrap(innerW-4),
	)
	if err == nil {
		m.renderer = r
	}
	m.cache = map[string]string{} // width changed — drop cached renders
}

func (m *Model) sidebarWidth() int {
	if m.width < minSidebar {
		return 0
	}
	return 28
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
	main := m.mainColumn()
	if m.sidebarWidth() == 0 {
		return main
	}
	side := m.st.sidebar.Width(m.sidebarWidth() - 1).Height(m.height).Render(m.sidebar())
	return lipgloss.JoinHorizontal(lipgloss.Top, side, main)
}

// mainColumn stacks header, transcript, input, and status.
func (m *Model) mainColumn() string {
	pad := lipgloss.NewStyle().Padding(0, 1)
	w := m.vp.Width

	// Header bar: title on the left, a context pill on the right.
	title := m.title
	if title == "" {
		title = "New conversation"
	}
	left := m.st.header.Render(title)
	if m.sessionID != "" {
		left += " " + m.st.headerDim.Render(shortID(m.sessionID))
	}
	right := m.st.tokenPill.Render(fmt.Sprintf(" %d↑ %d↓ ", m.tokensIn, m.tokensOut))
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
		right = ""
	}
	headerLine := left + strings.Repeat(" ", gap) + right
	header := pad.Render(headerLine) + "\n" + pad.Render(m.st.rule.Render(strings.Repeat("─", maxi(w, 1))))

	body := pad.Render(m.vp.View())
	input := m.inputView()
	status := pad.Render(m.statusBar())

	if len(m.palette) > 0 {
		return lipgloss.JoinVertical(lipgloss.Left, header, body, pad.Render(m.paletteView()), input, status)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, input, status)
}

func (m *Model) inputView() string {
	box := m.st.inputBox
	if !m.busy {
		box = m.st.inputFocus
	}
	field := lipgloss.JoinHorizontal(lipgloss.Top, m.st.prompt.Render("❯ "), m.ta.View())
	return lipgloss.NewStyle().Padding(0, 1).Render(box.Width(m.vp.Width - 2).Render(field))
}

func (m *Model) sidebar() string {
	var b strings.Builder
	b.WriteString(m.st.logo.Render("◆ antares"))
	b.WriteString("  " + m.st.headerDim.Render(version.Version) + "\n\n")

	// live status dot
	if m.busy {
		b.WriteString(m.st.stRunning.Render("● ") + m.st.sideValue.Render("working") + "\n\n")
	} else {
		b.WriteString(m.st.stDone.Render("● ") + m.st.sideValue.Render("ready") + "\n\n")
	}

	section := func(label, value string) {
		b.WriteString(m.st.sideLabel.Render(strings.ToUpper(label)) + "\n")
		b.WriteString(m.st.sideValue.Render(value) + "\n\n")
	}
	model, provider := "—", ""
	if m.cfg != nil {
		model, provider = firstNon(m.cfg.Model.Default, "—"), m.cfg.Model.Provider
	}
	section("Model", truncate(model, 22))
	if provider != "" {
		section("Provider", provider)
	}
	sess := "new"
	if m.title != "" {
		sess = m.title
	}
	section("Session", truncate(sess, 22))
	section("Tokens", fmt.Sprintf("%d in / %d out", m.tokensIn, m.tokensOut))

	reason := m.st.stErr.Render("off")
	if m.showReasoning {
		reason = m.st.stDone.Render("on")
	}
	b.WriteString(m.st.sideLabel.Render("REASONING") + "\n" + reason + "\n\n")

	// Shortcuts (trimmed on short terminals).
	b.WriteString(m.st.sideLabel.Render("SHORTCUTS") + "\n")
	rows := [][2]string{
		{"Enter", "send"}, {"Ctrl+J", "newline"}, {"/", "commands"},
		{"Ctrl+R", "reasoning"}, {"Ctrl+L", "clear"}, {"PgUp/Dn", "scroll"}, {"Ctrl+C", "quit"},
	}
	if m.height < 24 {
		rows = rows[:4]
	}
	for _, s := range rows {
		b.WriteString(m.st.statusKey.Render(pad(s[0], 8)) + m.st.sideLabel.Render(s[1]) + "\n")
	}
	return b.String()
}

func (m *Model) statusBar() string {
	sep := m.st.statusSep.Render("  ")
	var parts []string
	if m.busy {
		parts = append(parts, m.spin.View()+m.st.status.Render("working"))
	} else {
		parts = append(parts, m.st.stDone.Render("●")+m.st.status.Render(" ready"))
	}
	if m.status != "" {
		parts = append(parts, m.st.statusSep.Render("·")+" "+m.st.status.Render(m.status))
	}
	if m.vp.TotalLineCount() > m.vp.Height && !m.vp.AtBottom() {
		parts = append(parts, m.st.scrollHint.Render(fmt.Sprintf("↑ %d%%", int(m.vp.ScrollPercent()*100))))
	}
	line := strings.Join(parts, sep)
	hint := m.st.statusKey.Render("/") + m.st.status.Render("help")
	gap := m.vp.Width - lipgloss.Width(line) - lipgloss.Width(hint)
	if gap < 1 {
		return truncate(line, maxi(m.vp.Width, 1))
	}
	return line + strings.Repeat(" ", gap) + hint
}

func (m *Model) paletteView() string {
	var rows []string
	for i, c := range m.palette {
		marker := "  "
		name := m.st.paletteName.Render("/" + c.Name)
		if i == m.paletteSel {
			marker = m.st.paletteSel.Render("❯ ")
			name = m.st.paletteSel.Render("/" + c.Name)
		}
		rows = append(rows, marker+name+"  "+m.st.paletteDesc.Render(c.Summary))
	}
	return m.st.paletteBox.Width(m.vp.Width - 2).Render(strings.Join(rows, "\n"))
}

// refreshTranscript rebuilds the viewport content, keeping it pinned to the
// newest line unless the user scrolled up.
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
	for i := range m.blocks {
		bl := m.blocks[i]
		if bl.kind == blockReasoning && !m.showReasoning {
			continue
		}
		out = append(out, m.renderBlockCached(bl))
	}
	return strings.Join(out, "\n\n")
}

// renderBlockCached memoises rendered output for settled blocks so streaming
// stays smooth even with a long transcript (Glamour is the expensive part).
func (m *Model) renderBlockCached(bl block) string {
	if bl.streaming || (bl.kind == blockAssistant && !bl.done) {
		return m.renderBlock(bl)
	}
	key := fmt.Sprintf("%d|%d|%v|%v|%s|%s", bl.kind, m.vp.Width, bl.done, bl.isError, bl.title, bl.text)
	if m.cache == nil {
		m.cache = map[string]string{}
	}
	if s, ok := m.cache[key]; ok {
		return s
	}
	s := m.renderBlock(bl)
	m.cache[key] = s
	return s
}

func (m *Model) renderBlock(bl block) string {
	cw := m.vp.Width
	switch bl.kind {
	case blockUser:
		badge := m.st.userBadge.Render("❯ You")
		card := m.st.userCard.Width(cw - 2).Render(m.st.userText.Render(wrap(bl.text, cw-6)))
		return badge + "\n" + card

	case blockAssistant:
		badge := m.st.asstBadge.Render("◆ antares")
		return badge + "\n" + m.st.asstBar.Render(m.markdown(bl.text, cw-4))

	case blockReasoning:
		body := m.st.reasoning.Render(wrap(bl.text, cw-4))
		return m.st.reasonBadge.Render("… thinking") + "\n" + indent(body, m.st.reasoning.Render("▏ "))

	case blockTool:
		dot, label := m.toolStatus(bl)
		head := m.st.toolMeta.Render("⚙ "+bl.title) + "  " + dot
		body := strings.TrimSpace(bl.text)
		if body == "" {
			return m.st.toolCard.Width(cw - 2).Render(head + "  " + m.st.system.Render(label))
		}
		res := m.st.toolResult.Render(clampLines(body, 10, cw-6))
		return m.st.toolCard.Width(cw - 2).Render(head + "\n" + res)

	case blockNotice:
		return m.st.notice.Render(wrap(bl.text, cw-4))

	case blockError:
		return m.st.errorCard.Width(cw - 2).Render(m.st.stErr.Render("✗ ") + wrap(bl.text, cw-6))

	case blockSystem:
		return m.st.system.Render(wrap(bl.text, cw))
	}
	return bl.text
}

func (m *Model) toolStatus(bl block) (dot, label string) {
	switch {
	case bl.isError:
		return m.st.stErr.Render("✗ error"), "error"
	case bl.done:
		return m.st.stDone.Render("✓ done"), "done"
	default:
		return m.st.stRunning.Render("● running"), "running…"
	}
}

func (m *Model) markdown(text string, w int) string {
	if m.renderer != nil {
		if s, err := m.renderer.Render(text); err == nil {
			return strings.TrimRight(s, "\n")
		}
	}
	return wrap(text, w)
}

// ---- helpers ----------------------------------------------------------------

func wrap(s string, w int) string {
	if w < 4 {
		w = 4
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func clampLines(s string, maxLines, w int) string {
	s = wrap(s, w)
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		return strings.Join(lines, "\n") + "\n…"
	}
	return strings.Join(lines, "\n")
}

func pad(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
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
