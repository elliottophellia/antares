// Package tui implements the full-screen terminal interface: a scrollback
// transcript, a multiline composer, slash-command completion, and live tool
// output while the agent works.
package tui

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ANSI control sequences. Kept as constants so the render path reads as layout
// rather than escape-code soup.
const (
	escAltScreenOn  = "\x1b[?1049h"
	escAltScreenOff = "\x1b[?1049l"
	escCursorHide   = "\x1b[?25l"
	escCursorShow   = "\x1b[?25h"
	escClear        = "\x1b[2J"
	escHome         = "\x1b[H"
	escLineClear    = "\x1b[2K"
	escReset        = "\x1b[0m"
	escBold         = "\x1b[1m"
	escDim          = "\x1b[2m"
	escItalic       = "\x1b[3m"
	escUnderline    = "\x1b[4m"
	escReverse      = "\x1b[7m"
)

// Palette mirrors the dashboard: near-black with a soft red accent.
const (
	colPrimary   = "\x1b[38;5;203m" // soft red
	colMuted     = "\x1b[38;5;245m"
	colFaint     = "\x1b[38;5;240m"
	colSuccess   = "\x1b[38;5;114m"
	colWarning   = "\x1b[38;5;179m"
	colError     = "\x1b[38;5;203m"
	colUser      = "\x1b[38;5;110m"
	colAssistant = "\x1b[38;5;252m"
	colTool      = "\x1b[38;5;139m"
)

// screen accumulates one frame and writes it in a single syscall, which stops
// the transcript from tearing while text streams in.
type screen struct {
	b      strings.Builder
	width  int
	height int
}

func newScreen(width, height int) *screen {
	return &screen{width: width, height: height}
}

func (s *screen) moveTo(row, col int) {
	fmt.Fprintf(&s.b, "\x1b[%d;%dH", row, col)
}

// line writes one row, clearing the rest of it so stale text cannot linger.
func (s *screen) line(row int, text string) {
	s.moveTo(row, 1)
	s.b.WriteString(escLineClear)
	s.b.WriteString(truncateVisible(text, s.width))
	s.b.WriteString(escReset)
}

func (s *screen) flush(w io.Writer) error {
	_, err := io.WriteString(w, s.b.String())
	return err
}

// visibleWidth counts printable columns, ignoring ANSI sequences and counting
// wide runes as two columns.
func visibleWidth(s string) int {
	width, inEscape := 0, false
	for _, r := range s {
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			continue
		}
		width += runeWidth(r)
	}
	return width
}

// runeWidth is a pragmatic east-asian width check: enough for CJK and emoji in
// a transcript without pulling in a full width table.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.IsControl(r):
		return 0
	case r >= 0x1100 && (r <= 0x115F || // Hangul Jamo
		r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) || // CJK
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul syllables
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1F64F) || // emoji
		(r >= 0x1F900 && r <= 0x1F9FF) ||
		(r >= 0x20000 && r <= 0x3FFFD)):
		return 2
	}
	return 1
}

// truncateVisible clips to a column budget while preserving escape sequences.
func truncateVisible(s string, max int) string {
	if max <= 0 {
		return ""
	}
	var b strings.Builder
	width, inEscape := 0, false
	for _, r := range s {
		if inEscape {
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			b.WriteRune(r)
			continue
		}
		w := runeWidth(r)
		if width+w > max {
			break
		}
		b.WriteRune(r)
		width += w
	}
	return b.String()
}

// wrap breaks plain text into lines of at most width columns, preferring word
// boundaries and preserving existing newlines.
func wrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var out []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		line := ""
		lineWidth := 0
		for _, word := range strings.Fields(paragraph) {
			wordWidth := visibleWidth(word)
			switch {
			case lineWidth == 0:
				line, lineWidth = word, wordWidth
			case lineWidth+1+wordWidth <= width:
				line += " " + word
				lineWidth += 1 + wordWidth
			default:
				out = append(out, line)
				line, lineWidth = word, wordWidth
			}
			// A single word longer than the line gets hard-split.
			for lineWidth > width {
				out = append(out, truncateVisible(line, width))
				line = trimVisible(line, width)
				lineWidth = visibleWidth(line)
			}
		}
		out = append(out, line)
	}
	return out
}

// trimVisible drops the first n visible columns.
func trimVisible(s string, n int) string {
	width := 0
	for i, r := range s {
		if width >= n {
			return s[i:]
		}
		width += runeWidth(r)
	}
	return ""
}

// padRight pads to a column count, accounting for escape sequences.
func padRight(s string, width int) string {
	gap := width - visibleWidth(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// runeLen counts runes, used by the editor for cursor arithmetic.
func runeLen(s string) int { return utf8.RuneCountInString(s) }
