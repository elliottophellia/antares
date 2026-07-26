package tui

import (
	"bufio"
	"strings"
	"unicode/utf8"
)

// keyKind enumerates the decoded key events the loop reacts to.
type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyNewline // Shift/Alt+Enter: insert a line break instead of sending
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyTab
	keyShiftTab
	keyEscape
	keyCtrlC
	keyCtrlD
	keyCtrlL
	keyCtrlU
	keyCtrlK
	keyCtrlW
	keyCtrlA
	keyCtrlE
	keyPageUp
	keyPageDown
	keyUnknown
)

type keyEvent struct {
	kind keyKind
	r    rune
}

// readKey decodes one key press, including the escape sequences terminals use
// for arrows, navigation, and modified Enter.
func readKey(r *bufio.Reader) (keyEvent, error) {
	b, err := r.ReadByte()
	if err != nil {
		return keyEvent{}, err
	}

	switch b {
	case 0x03:
		return keyEvent{kind: keyCtrlC}, nil
	case 0x04:
		return keyEvent{kind: keyCtrlD}, nil
	case 0x01:
		return keyEvent{kind: keyCtrlA}, nil
	case 0x05:
		return keyEvent{kind: keyCtrlE}, nil
	case 0x0B:
		return keyEvent{kind: keyCtrlK}, nil
	case 0x0C:
		return keyEvent{kind: keyCtrlL}, nil
	case 0x15:
		return keyEvent{kind: keyCtrlU}, nil
	case 0x17:
		return keyEvent{kind: keyCtrlW}, nil
	case '\r', '\n':
		return keyEvent{kind: keyEnter}, nil
	case '\t':
		return keyEvent{kind: keyTab}, nil
	case 0x7F, 0x08:
		return keyEvent{kind: keyBackspace}, nil
	case 0x1b:
		return readEscape(r)
	}

	// UTF-8: pull the continuation bytes so multi-byte runes arrive intact.
	if b < 0x80 {
		return keyEvent{kind: keyRune, r: rune(b)}, nil
	}
	buf := []byte{b}
	for len(buf) < 4 {
		next, err := r.ReadByte()
		if err != nil {
			break
		}
		buf = append(buf, next)
		if ru, size := utf8.DecodeRune(buf); ru != utf8.RuneError || size > 1 {
			return keyEvent{kind: keyRune, r: ru}, nil
		}
	}
	return keyEvent{kind: keyUnknown}, nil
}

// readEscape handles CSI and SS3 sequences after an ESC byte.
func readEscape(r *bufio.Reader) (keyEvent, error) {
	// A lone ESC (nothing buffered behind it) means the user pressed Escape.
	if r.Buffered() == 0 {
		return keyEvent{kind: keyEscape}, nil
	}
	b, err := r.ReadByte()
	if err != nil {
		return keyEvent{kind: keyEscape}, nil
	}

	switch b {
	case '\r', '\n':
		return keyEvent{kind: keyNewline}, nil // Alt+Enter
	case 'O':
		c, err := r.ReadByte()
		if err != nil {
			return keyEvent{kind: keyUnknown}, nil
		}
		switch c {
		case 'A':
			return keyEvent{kind: keyUp}, nil
		case 'B':
			return keyEvent{kind: keyDown}, nil
		case 'C':
			return keyEvent{kind: keyRight}, nil
		case 'D':
			return keyEvent{kind: keyLeft}, nil
		case 'H':
			return keyEvent{kind: keyHome}, nil
		case 'F':
			return keyEvent{kind: keyEnd}, nil
		}
		return keyEvent{kind: keyUnknown}, nil
	case '[':
		var params strings.Builder
		for {
			c, err := r.ReadByte()
			if err != nil {
				return keyEvent{kind: keyUnknown}, nil
			}
			if (c >= '0' && c <= '9') || c == ';' {
				params.WriteByte(c)
				continue
			}
			return decodeCSI(c, params.String()), nil
		}
	}
	return keyEvent{kind: keyUnknown}, nil
}

func decodeCSI(final byte, params string) keyEvent {
	// "1;2" style parameters carry modifiers; 2 = Shift, 3 = Alt.
	modified := strings.Contains(params, ";")

	switch final {
	case 'A':
		return keyEvent{kind: keyUp}
	case 'B':
		return keyEvent{kind: keyDown}
	case 'C':
		return keyEvent{kind: keyRight}
	case 'D':
		return keyEvent{kind: keyLeft}
	case 'H':
		return keyEvent{kind: keyHome}
	case 'F':
		return keyEvent{kind: keyEnd}
	case 'Z':
		return keyEvent{kind: keyShiftTab}
	case 'u':
		// Kitty keyboard protocol: "13;2u" is Shift+Enter.
		if strings.HasPrefix(params, "13") && modified {
			return keyEvent{kind: keyNewline}
		}
		return keyEvent{kind: keyUnknown}
	case '~':
		switch strings.SplitN(params, ";", 2)[0] {
		case "1", "7":
			return keyEvent{kind: keyHome}
		case "3":
			return keyEvent{kind: keyDelete}
		case "4", "8":
			return keyEvent{kind: keyEnd}
		case "5":
			return keyEvent{kind: keyPageUp}
		case "6":
			return keyEvent{kind: keyPageDown}
		}
	}
	return keyEvent{kind: keyUnknown}
}

// editor is the multiline composer: a rune buffer plus a cursor.
type editor struct {
	runes  []rune
	cursor int
	// history holds previously sent messages for up/down recall.
	history []string
	histIdx int
	draft   string
}

func (e *editor) text() string { return string(e.runes) }

func (e *editor) setText(s string) {
	e.runes = []rune(s)
	e.cursor = len(e.runes)
}

func (e *editor) clear() {
	e.runes = e.runes[:0]
	e.cursor = 0
	e.histIdx = len(e.history)
}

func (e *editor) insert(r rune) {
	e.runes = append(e.runes, 0)
	copy(e.runes[e.cursor+1:], e.runes[e.cursor:])
	e.runes[e.cursor] = r
	e.cursor++
}

func (e *editor) insertString(s string) {
	for _, r := range s {
		e.insert(r)
	}
}

func (e *editor) backspace() {
	if e.cursor == 0 {
		return
	}
	e.runes = append(e.runes[:e.cursor-1], e.runes[e.cursor:]...)
	e.cursor--
}

func (e *editor) deleteForward() {
	if e.cursor >= len(e.runes) {
		return
	}
	e.runes = append(e.runes[:e.cursor], e.runes[e.cursor+1:]...)
}

// deleteWord removes the word before the cursor (Ctrl+W).
func (e *editor) deleteWord() {
	i := e.cursor
	for i > 0 && e.runes[i-1] == ' ' {
		i--
	}
	for i > 0 && e.runes[i-1] != ' ' && e.runes[i-1] != '\n' {
		i--
	}
	e.runes = append(e.runes[:i], e.runes[e.cursor:]...)
	e.cursor = i
}

// killToStart / killToEnd implement Ctrl+U and Ctrl+K within the current line.
func (e *editor) killToStart() {
	start := e.lineStart()
	e.runes = append(e.runes[:start], e.runes[e.cursor:]...)
	e.cursor = start
}

func (e *editor) killToEnd() {
	end := e.lineEnd()
	e.runes = append(e.runes[:e.cursor], e.runes[end:]...)
}

func (e *editor) lineStart() int {
	i := e.cursor
	for i > 0 && e.runes[i-1] != '\n' {
		i--
	}
	return i
}

func (e *editor) lineEnd() int {
	i := e.cursor
	for i < len(e.runes) && e.runes[i] != '\n' {
		i++
	}
	return i
}

func (e *editor) left() {
	if e.cursor > 0 {
		e.cursor--
	}
}

func (e *editor) right() {
	if e.cursor < len(e.runes) {
		e.cursor++
	}
}

// pushHistory records a sent message for later recall.
func (e *editor) pushHistory(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if n := len(e.history); n > 0 && e.history[n-1] == s {
		e.histIdx = len(e.history)
		return
	}
	e.history = append(e.history, s)
	if len(e.history) > 500 {
		e.history = e.history[len(e.history)-500:]
	}
	e.histIdx = len(e.history)
}

// historyPrev walks backwards, stashing the in-progress draft on first use.
func (e *editor) historyPrev() bool {
	if len(e.history) == 0 || e.histIdx == 0 {
		return false
	}
	if e.histIdx == len(e.history) {
		e.draft = e.text()
	}
	e.histIdx--
	e.setText(e.history[e.histIdx])
	return true
}

func (e *editor) historyNext() bool {
	if e.histIdx >= len(e.history) {
		return false
	}
	e.histIdx++
	if e.histIdx == len(e.history) {
		e.setText(e.draft)
		return true
	}
	e.setText(e.history[e.histIdx])
	return true
}

// lines splits the buffer for rendering and returns the cursor's row/column.
func (e *editor) lines() (rows []string, cursorRow, cursorCol int) {
	text := e.text()
	rows = strings.Split(text, "\n")
	before := string(e.runes[:e.cursor])
	cursorRow = strings.Count(before, "\n")
	lastNL := strings.LastIndexByte(before, '\n')
	cursorCol = visibleWidth(before[lastNL+1:])
	return rows, cursorRow, cursorCol
}
