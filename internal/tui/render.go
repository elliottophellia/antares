package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Layout constants. The composer grows with its content up to composerMaxRows.
const (
	composerMaxRows = 8
	paletteMaxRows  = 6
	gutter          = 2
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// transcriptHeight is the rows available for scrollback after chrome.
func (m *Model) transcriptHeight() int {
	h := m.height - 1 /*header*/ - 1 /*status*/ - m.composerRows() - 1 /*separator*/
	if len(m.palette) > 0 {
		h -= min(len(m.palette), paletteMaxRows) + 1
	}
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) composerRows() int {
	rows, _, _ := m.ed.lines()
	// Long lines wrap, so count the wrapped height rather than the raw lines.
	total := 0
	for _, r := range rows {
		total += max(1, (visibleWidth(r)+m.contentWidth()-1)/max(1, m.contentWidth()))
	}
	return clamp(total, 1, composerMaxRows)
}

func (m *Model) contentWidth() int { return max(20, m.width-gutter*2) }

// render paints one full frame.
func (m *Model) render() {
	m.mu.Lock()
	blocks := make([]block, len(m.blocks))
	copy(blocks, m.blocks)
	m.mu.Unlock()

	s := newScreen(m.width, m.height)
	s.b.WriteString(escCursorHide)

	m.renderHeader(s)

	lines := m.renderBlocks(blocks)
	visible := m.transcriptHeight()

	// scroll counts lines up from the bottom.
	maxScroll := max(0, len(lines)-visible)
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	start := max(0, len(lines)-visible-m.scroll)
	end := min(len(lines), start+visible)

	row := 2
	for i := start; i < end; i++ {
		s.line(row, lines[i])
		row++
	}
	for ; row < 2+visible; row++ {
		s.line(row, "")
	}

	m.renderComposer(s, 2+visible)
	m.renderStatus(s)

	if err := s.flush(m.out); err != nil {
		return
	}
	m.placeCursor()
}

func (m *Model) renderHeader(s *screen) {
	title := m.title
	if title == "" {
		title = "New conversation"
	}
	left := fmt.Sprintf("%s%s%s %s%s", colPrimary, "✳", escReset, escBold, title)
	right := ""
	if m.cfg.Model.Default != "" {
		right = colFaint + m.cfg.Model.Default + escReset
	}
	gap := m.width - visibleWidth(left) - visibleWidth(right) - 2
	if gap < 1 {
		right, gap = "", m.width-visibleWidth(left)-2
	}
	s.line(1, " "+left+strings.Repeat(" ", max(0, gap))+right+" ")
}

// renderBlocks flattens the transcript into wrapped, coloured lines.
func (m *Model) renderBlocks(blocks []block) []string {
	width := m.contentWidth()
	var out []string
	pad := strings.Repeat(" ", gutter)

	for i, b := range blocks {
		if i > 0 {
			out = append(out, "")
		}
		switch b.kind {
		case blockUser:
			for j, l := range wrap(b.text, width-2) {
				prefix := colUser + "› " + escReset
				if j > 0 {
					prefix = "  "
				}
				out = append(out, pad+prefix+colUser+l+escReset)
			}

		case blockAssistant:
			for _, l := range renderMarkdown(b.text, width) {
				out = append(out, pad+l)
			}
			if b.streaming {
				out = append(out, pad+colFaint+"▌"+escReset)
			}

		case blockReasoning:
			out = append(out, pad+colFaint+escItalic+"thinking"+escReset)
			for _, l := range wrap(b.text, width-2) {
				out = append(out, pad+"  "+colFaint+escItalic+l+escReset)
			}

		case blockTool:
			icon, tone := "•", colTool
			switch {
			case b.isError:
				icon, tone = "✗", colError
			case b.done:
				icon, tone = "✓", colSuccess
			case b.streaming:
				icon = spinnerFrames[m.spinner%len(spinnerFrames)]
			}
			out = append(out, pad+tone+icon+" "+escReset+colMuted+truncateVisible(b.title, width-3)+escReset)

			// Tool output is indented and capped; the full text lives in the log.
			body := strings.TrimRight(b.text, "\n")
			if body != "" {
				lines := wrap(body, width-4)
				const maxToolLines = 12
				if len(lines) > maxToolLines {
					shown := lines[:maxToolLines]
					for _, l := range shown {
						out = append(out, pad+"  "+colFaint+l+escReset)
					}
					out = append(out, pad+"  "+colFaint+fmt.Sprintf("… %d more lines", len(lines)-maxToolLines)+escReset)
				} else {
					for _, l := range lines {
						out = append(out, pad+"  "+colFaint+l+escReset)
					}
				}
			}

		case blockNotice:
			for _, l := range wrap(b.text, width-2) {
				out = append(out, pad+colWarning+"! "+l+escReset)
			}

		case blockError:
			for _, l := range wrap(b.text, width-2) {
				out = append(out, pad+colError+"✗ "+l+escReset)
			}

		case blockSystem:
			for _, l := range wrap(b.text, width) {
				out = append(out, pad+colFaint+l+escReset)
			}
		}
	}
	return out
}

// renderMarkdown gives fenced code and headings a distinct look without
// pulling in a full markdown engine.
func renderMarkdown(text string, width int) []string {
	var out []string
	inCode := false
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			lang := strings.TrimPrefix(trimmed, "```")
			if inCode && lang != "" {
				out = append(out, colFaint+"┌ "+lang+escReset)
			} else {
				out = append(out, colFaint+"└"+escReset)
			}
			continue
		}
		if inCode {
			out = append(out, colFaint+"│ "+escReset+colAssistant+truncateVisible(raw, width-2)+escReset)
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimLeft(trimmed, "# ")
			out = append(out, escBold+truncateVisible(heading, width)+escReset)
			continue
		}
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		for _, l := range wrap(raw, width) {
			out = append(out, colAssistant+inlineMarkup(l)+escReset)
		}
	}
	return out
}

// inlineMarkup highlights `code` and **bold** spans.
func inlineMarkup(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				b.WriteString(colPrimary + s[i+1:i+1+end] + escReset + colAssistant)
				i += end + 2
				continue
			}
		case strings.HasPrefix(s[i:], "**"):
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				b.WriteString(escBold + s[i+2:i+2+end] + escReset + colAssistant)
				i += end + 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// renderComposer draws the separator, palette, and input area.
func (m *Model) renderComposer(s *screen, startRow int) {
	row := startRow
	s.line(row, colFaint+strings.Repeat("─", m.width)+escReset)
	row++

	if len(m.palette) > 0 {
		shown := m.palette
		if len(shown) > paletteMaxRows {
			shown = shown[:paletteMaxRows]
		}
		for i, c := range shown {
			marker, tone := "  ", colMuted
			if i == m.paletteSel {
				marker, tone = colPrimary+"▸ "+escReset, escBold
			}
			line := fmt.Sprintf("%s%s%s%s  %s%s%s",
				marker, tone, padRight("/"+c.Name, 16), escReset, colFaint, c.Summary, escReset)
			s.line(row, " "+line)
			row++
		}
	}

	rows, cursorRow, _ := m.ed.lines()
	for i := 0; i < m.composerRows(); i++ {
		prompt := colPrimary + "❯ " + escReset
		if i > 0 {
			prompt = "  "
		}
		text := ""
		if i < len(rows) {
			text = rows[i]
		}
		if i == 0 && text == "" && cursorRow == 0 && !m.busy {
			text = colFaint + "Send a message, or /help" + escReset
		}
		s.line(row, " "+prompt+text)
		row++
	}
}

func (m *Model) renderStatus(s *screen) {
	left := ""
	if m.busy {
		left = colPrimary + spinnerFrames[m.spinner%len(spinnerFrames)] + escReset + colMuted + " working… Ctrl+C to interrupt" + escReset
	} else if m.statusMsg != "" {
		left = colMuted + m.statusMsg + escReset
	} else {
		left = colFaint + "Enter send · Alt+Enter newline · / commands · Ctrl+C quit" + escReset
	}

	right := ""
	if m.sessionID != "" {
		right = colFaint + m.sessionID[:min(12, len(m.sessionID))] + escReset
	}
	if m.scroll > 0 {
		right = colWarning + fmt.Sprintf("↑%d", m.scroll) + escReset + "  " + right
	}

	gap := m.width - visibleWidth(left) - visibleWidth(right) - 2
	if gap < 1 {
		right, gap = "", m.width-visibleWidth(left)-2
	}
	s.line(m.height, " "+left+strings.Repeat(" ", max(0, gap))+right+" ")
}

// placeCursor puts the real terminal cursor in the composer so typing feels native.
func (m *Model) placeCursor() {
	_, cursorRow, cursorCol := m.ed.lines()
	base := 2 + m.transcriptHeight() + 1 // header + transcript + separator
	if len(m.palette) > 0 {
		base += min(len(m.palette), paletteMaxRows)
	}
	row := base + clamp(cursorRow, 0, m.composerRows()-1)
	col := 4 + cursorCol // leading space + "❯ "
	fmt.Fprintf(m.out, "\x1b[%d;%dH%s", row, col, escCursorShow)
}

// summarizeArgs renders the interesting argument of a tool call.
func summarizeArgs(name, raw string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k].(string); ok && v != "" {
				return strings.ReplaceAll(v, "\n", " ")
			}
		}
		return ""
	}
	switch name {
	case "terminal":
		return pick("command")
	case "read_file", "write_file", "edit_file", "list_files":
		return pick("path")
	case "grep", "glob":
		return pick("pattern")
	case "web_search", "rag_search", "session_search":
		return pick("query")
	case "web_fetch":
		return pick("url")
	case "memory", "skill", "todo":
		return pick("action")
	}
	return pick("path", "query", "name", "command")
}

func min(a, b int) int {
	if a < b {
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

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

var _ = time.Now
