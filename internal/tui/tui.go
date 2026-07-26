package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

// blockKind distinguishes transcript entries so each renders in its own voice.
type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockReasoning
	blockTool
	blockNotice
	blockError
	blockSystem
)

// block is one entry in the scrollback.
type block struct {
	kind blockKind
	// title labels tool blocks ("terminal: ls -la").
	title string
	text  string
	// streaming marks the block currently being appended to.
	streaming bool
	done      bool
	isError   bool
}

// Model is the TUI state. All mutation happens on the event loop goroutine
// except agent events, which arrive on evCh and are applied there too.
type Model struct {
	agent *agent.Agent
	cfg   *config.Config
	db    store.Store

	out    *os.File
	in     *bufio.Reader
	width  int
	height int

	blocks    []block
	scroll    int // lines scrolled up from the bottom; 0 pins to the newest
	ed        editor
	sessionID string
	title     string

	busy      bool
	cancel    context.CancelFunc
	spinner   int
	statusMsg string
	statusAt  time.Time

	// completion state for the slash palette
	palette    []Command
	paletteSel int

	mu sync.Mutex
}

// New builds a TUI bound to a running agent.
func New(ag *agent.Agent, cfg *config.Config, db store.Store) *Model {
	return &Model{
		agent: ag, cfg: cfg, db: db,
		out: os.Stdout,
		in:  bufio.NewReaderSize(os.Stdin, 4096),
	}
}

// Run takes over the terminal until the user quits.
func (m *Model) Run(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("the TUI needs an interactive terminal (try `antares chat \"…\"` instead)")
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("entering raw mode: %w", err)
	}
	// Restoring the terminal matters more than any error path below, so it is
	// deferred first and also wired to signals.
	restore := func() {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
		fmt.Fprint(m.out, escCursorShow+escAltScreenOff+escReset)
	}
	defer restore()

	fmt.Fprint(m.out, escAltScreenOn+escClear+escHome+escCursorHide)
	m.refreshSize()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// SIGWINCH keeps the layout correct when the window is resized.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	m.greet()
	m.render()

	keys := make(chan keyEvent, 32)
	keyErr := make(chan error, 1)
	go func() {
		for {
			k, err := readKey(m.in)
			if err != nil {
				keyErr <- err
				return
			}
			keys <- k
		}
	}()

	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-winch:
			m.refreshSize()
			m.render()
		case <-keyErr:
			return nil
		case <-ticker.C:
			// Only repaint while something is moving, to keep idle CPU at zero.
			if m.busy {
				m.spinner++
				m.render()
			} else if !m.statusAt.IsZero() && time.Since(m.statusAt) > 4*time.Second {
				m.statusMsg = ""
				m.statusAt = time.Time{}
				m.render()
			}
		case k := <-keys:
			quit, err := m.handleKey(ctx, k)
			if err != nil {
				return err
			}
			if quit {
				return nil
			}
			m.render()
		}
	}
}

func (m *Model) refreshSize() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		w, h = 80, 24
	}
	m.width, m.height = w, h
}

func (m *Model) greet() {
	m.push(block{
		kind: blockSystem,
		text: fmt.Sprintf("%s %s — type a message, or /help for commands.", version.Display, version.Version),
	})
	provider, model := m.cfg.Model.Provider, m.cfg.Model.Default
	if model == "" {
		m.push(block{
			kind: blockNotice,
			text: "No model selected yet. Run /setup, or set model.default in the dashboard.",
		})
	} else {
		m.push(block{kind: blockSystem, text: fmt.Sprintf("Model: %s · %s", model, provider)})
	}
}

func (m *Model) push(b block) {
	m.mu.Lock()
	m.blocks = append(m.blocks, b)
	m.mu.Unlock()
}

// appendTo streams text into the last block of a kind, opening one if needed.
func (m *Model) appendTo(kind blockKind, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == kind && m.blocks[n-1].streaming {
		m.blocks[n-1].text += text
		return
	}
	m.blocks = append(m.blocks, block{kind: kind, text: text, streaming: true})
}

func (m *Model) closeStreaming() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.blocks {
		m.blocks[i].streaming = false
	}
}

// setStatus shows a transient message in the status bar.
func (m *Model) setStatus(format string, args ...any) {
	m.statusMsg = fmt.Sprintf(format, args...)
	m.statusAt = time.Now()
}

// handleKey advances the UI for one key press; it reports whether to quit.
func (m *Model) handleKey(ctx context.Context, k keyEvent) (bool, error) {
	// The palette takes priority so Tab/arrows complete rather than edit.
	if len(m.palette) > 0 {
		switch k.kind {
		case keyTab:
			// One candidate means there is nothing to choose between: complete it.
			if len(m.palette) == 1 {
				m.acceptCompletion()
				return false, nil
			}
			m.paletteSel = (m.paletteSel + 1) % len(m.palette)
			return false, nil
		case keyDown:
			m.paletteSel = (m.paletteSel + 1) % len(m.palette)
			return false, nil
		case keyShiftTab, keyUp:
			m.paletteSel = (m.paletteSel - 1 + len(m.palette)) % len(m.palette)
			return false, nil
		case keyEnter:
			// A fully typed command runs; a partial one completes first. Making
			// Enter always complete would mean pressing it twice for every
			// command, which is what it felt like.
			typed := strings.TrimPrefix(strings.TrimSpace(m.ed.text()), "/")
			if !commandExists(typed) {
				m.acceptCompletion()
				return false, nil
			}
			m.palette = nil
		case keyEscape:
			m.palette = nil
			return false, nil
		}
	}

	switch k.kind {
	case keyCtrlC:
		if m.busy {
			m.interrupt()
			return false, nil
		}
		if m.ed.text() != "" {
			m.ed.clear()
			m.palette = nil
			return false, nil
		}
		return true, nil

	case keyCtrlD:
		if m.ed.text() == "" {
			return true, nil
		}
		m.ed.deleteForward()

	case keyCtrlL:
		m.mu.Lock()
		m.blocks = nil
		m.mu.Unlock()
		m.scroll = 0
		m.greet()

	case keyEnter:
		text := strings.TrimSpace(m.ed.text())
		if text == "" {
			return false, nil
		}
		if m.busy {
			m.setStatus("still working — press Ctrl+C to interrupt")
			return false, nil
		}
		m.ed.pushHistory(text)
		m.ed.clear()
		m.palette = nil
		m.scroll = 0

		if strings.HasPrefix(text, "/") {
			quit := m.runCommand(ctx, text)
			return quit, nil
		}
		m.send(ctx, text)

	case keyNewline:
		m.ed.insert('\n')

	case keyBackspace:
		m.ed.backspace()
		m.updatePalette()
	case keyDelete:
		m.ed.deleteForward()
	case keyLeft:
		m.ed.left()
	case keyRight:
		m.ed.right()
	case keyHome, keyCtrlA:
		m.ed.cursor = m.ed.lineStart()
	case keyEnd, keyCtrlE:
		m.ed.cursor = m.ed.lineEnd()
	case keyCtrlU:
		m.ed.killToStart()
		m.updatePalette()
	case keyCtrlK:
		m.ed.killToEnd()
	case keyCtrlW:
		m.ed.deleteWord()
		m.updatePalette()

	case keyUp:
		// Recall history only from a single-line buffer; otherwise move within it.
		if !strings.Contains(m.ed.text(), "\n") {
			m.ed.historyPrev()
		} else {
			m.scrollBy(-1)
		}
	case keyDown:
		if !strings.Contains(m.ed.text(), "\n") {
			m.ed.historyNext()
		} else {
			m.scrollBy(1)
		}

	case keyPageUp:
		m.scrollBy(-(m.transcriptHeight() / 2))
	case keyPageDown:
		m.scrollBy(m.transcriptHeight() / 2)

	case keyTab:
		m.updatePalette()
		if len(m.palette) == 1 {
			m.acceptCompletion()
		}

	case keyEscape:
		m.palette = nil

	case keyRune:
		m.ed.insert(k.r)
		m.updatePalette()
	}
	return false, nil
}

func (m *Model) scrollBy(delta int) {
	m.scroll += -delta
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// interrupt cancels the in-flight turn.
func (m *Model) interrupt() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.sessionID != "" {
		m.agent.Interrupt(m.sessionID)
	}
	m.setStatus("interrupted")
}

// send runs one agent turn, streaming events into the transcript.
func (m *Model) send(ctx context.Context, text string) {
	m.push(block{kind: blockUser, text: text})
	m.busy = true

	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	go func() {
		defer func() {
			m.closeStreaming()
			m.busy = false
			m.cancel = nil
			cancel()
			m.render()
		}()

		_, err := m.agent.Run(runCtx, agent.Request{
			SessionID: m.sessionID,
			Message:   text,
			Platform:  "tui",
		}, func(e agent.Event) error {
			m.applyEvent(e)
			m.render()
			return nil
		})
		if err != nil && runCtx.Err() == nil {
			m.push(block{kind: blockError, text: err.Error()})
		}
	}()
}

// applyEvent folds one agent event into the transcript.
func (m *Model) applyEvent(e agent.Event) {
	switch e.Type {
	case agent.EventSession:
		if e.ID != "" {
			m.sessionID = e.ID
		}
		if e.Title != "" {
			m.title = e.Title
		}
	case agent.EventText:
		m.appendTo(blockAssistant, e.Delta)
	case agent.EventReasoning:
		if m.cfg.Display.ShowReasoning {
			m.appendTo(blockReasoning, e.Delta)
		}
	case agent.EventToolCall:
		m.closeStreaming()
		m.push(block{kind: blockTool, title: toolHeadline(e.Name, e.Arguments), streaming: true})
	case agent.EventToolProgress:
		m.appendTo(blockTool, e.Chunk)
	case agent.EventToolResult:
		m.mu.Lock()
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].kind == blockTool && !m.blocks[i].done {
				m.blocks[i].text = e.Content
				m.blocks[i].done = true
				m.blocks[i].isError = e.IsError
				m.blocks[i].streaming = false
				break
			}
		}
		m.mu.Unlock()
	case agent.EventNotice:
		m.push(block{kind: blockNotice, text: e.Message})
	case agent.EventError:
		m.push(block{kind: blockError, text: e.Err})
	case agent.EventUsage:
		m.setStatus("%d in / %d out tokens", e.InputTokens, e.OutputTokens)
	}
}

// toolHeadline renders a one-line summary of a tool call for the transcript.
func toolHeadline(name, args string) string {
	summary := summarizeArgs(name, args)
	if summary == "" {
		return name
	}
	return name + "  " + summary
}
