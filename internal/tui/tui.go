// Package tui is antares' terminal UI: a colourful, responsive, web-like chat
// built on Bubble Tea — sidebar, scrollable transcript with Markdown, a bordered
// input box, a slash-command palette, and toggleable reasoning. The Bubble Tea
// renderer owns the screen, so resizing and redraws never corrupt the layout.
package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

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
	kind      blockKind
	title     string
	text      string
	streaming bool
	done      bool
	isError   bool
}

// agent-event bridge messages.
type (
	evMsg   struct{ e agent.Event }
	doneMsg struct{ err error }
)

// Model is the Bubble Tea model.
type Model struct {
	ag  *agent.Agent
	cfg *config.Config
	db  store.Store

	st       styles
	renderer *glamour.TermRenderer

	width, height int
	ready         bool

	vp   viewport.Model
	ta   textarea.Model
	spin spinner.Model

	blocks    []block
	sessionID string
	title     string

	busy   bool
	cancel context.CancelFunc
	msgCh  chan tea.Msg

	tokensIn, tokensOut int
	showReasoning       bool
	status              string

	palette    []Command
	paletteSel int

	cache map[string]string // memoised block renders, keyed by content+width

	demo bool
}

// New builds a TUI bound to a running agent.
func New(ag *agent.Agent, cfg *config.Config, db store.Store) *Model {
	ta := textarea.New()
	ta.Placeholder = "Message antares…  (/ for commands, Enter to send, Ctrl+J for newline)"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.Focus()
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter sends; Ctrl+J adds a newline.

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &Model{
		ag: ag, cfg: cfg, db: db,
		st:            newStyles(),
		ta:            ta,
		spin:          sp,
		showReasoning: cfg != nil && cfg.Display.ShowReasoning,
	}
}

// Run takes over the terminal until the user quits.
func (m *Model) Run(ctx context.Context) error {
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	m.greet()
	return tea.Batch(textarea.Blink, m.spin.Tick)
}

func (m *Model) greet() {
	m.blocks = append(m.blocks, block{
		kind: blockSystem,
		text: version.Display + " " + version.Version + " — type a message, or /help for commands.",
	})
	if m.cfg != nil && m.cfg.Model.Default == "" && !m.demo {
		m.blocks = append(m.blocks, block{kind: blockNotice, text: "No model selected. Run /setup, or set model.default in the dashboard."})
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.ready = true
		m.refreshTranscript()
		return m, nil

	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil

	case evMsg:
		m.applyEvent(msg.e)
		m.refreshTranscript()
		return m, m.listen()

	case doneMsg:
		m.busy = false
		m.cancel = nil
		m.closeStreaming()
		if msg.err != nil && !strings.Contains(msg.err.Error(), "context canceled") {
			m.blocks = append(m.blocks, block{kind: blockError, text: msg.err.Error()})
		}
		m.refreshTranscript()
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)
	}

	// Forward anything else to the components.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	cmds = append(cmds, cmd)
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Palette navigation takes priority.
	if len(m.palette) > 0 {
		switch msg.Type {
		case tea.KeyUp, tea.KeyCtrlP:
			m.paletteSel = (m.paletteSel - 1 + len(m.palette)) % len(m.palette)
			return m, nil
		case tea.KeyDown, tea.KeyCtrlN:
			m.paletteSel = (m.paletteSel + 1) % len(m.palette)
			return m, nil
		case tea.KeyTab:
			m.acceptCompletion()
			return m, nil
		case tea.KeyEsc:
			m.palette = nil
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		if m.busy {
			m.interrupt()
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyCtrlD:
		return m, tea.Quit

	case tea.KeyCtrlL:
		m.blocks = nil
		m.greet()
		m.refreshTranscript()
		return m, nil

	case tea.KeyCtrlR:
		m.showReasoning = !m.showReasoning
		m.setStatus("reasoning " + onOff(m.showReasoning))
		m.refreshTranscript()
		return m, nil

	case tea.KeyPgUp:
		m.vp.HalfViewUp()
		return m, nil
	case tea.KeyPgDown:
		m.vp.HalfViewDown()
		return m, nil

	case tea.KeyEnter:
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return m, nil
		}
		if len(m.palette) > 0 {
			typed := strings.TrimPrefix(strings.Fields(text)[0], "/")
			if !commandExists(typed) {
				m.acceptCompletion()
				return m, nil
			}
			m.palette = nil
		}
		if m.busy {
			m.setStatus("still working — Ctrl+C to interrupt")
			return m, nil
		}
		m.ta.Reset()
		m.vp.GotoBottom()
		if strings.HasPrefix(text, "/") {
			quit, cmd := m.runCommand(text)
			m.refreshTranscript()
			if quit {
				return m, tea.Quit
			}
			return m, cmd
		}
		if m.demo {
			m.ta.Reset()
			m.demoReply(text)
			m.refreshTranscript()
			m.vp.GotoBottom()
			return m, nil
		}
		return m, m.startTurn(text)
	}

	// Normal editing.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.updatePalette()
	return m, cmd
}

// startTurn runs one agent turn, bridging its events onto the Bubble Tea loop.
func (m *Model) startTurn(text string) tea.Cmd {
	m.blocks = append(m.blocks, block{kind: blockUser, text: text})
	m.busy = true
	m.refreshTranscript()

	runCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.msgCh = make(chan tea.Msg, 128)
	ch := m.msgCh
	sess := m.sessionID

	go func() {
		_, err := m.ag.Run(runCtx, agent.Request{SessionID: sess, Message: text, Platform: "tui"},
			func(e agent.Event) error {
				select {
				case ch <- evMsg{e}:
				case <-runCtx.Done():
					return runCtx.Err()
				}
				return nil
			})
		ch <- doneMsg{err: err}
	}()
	return tea.Batch(m.listen(), m.spin.Tick)
}

// listen pulls the next bridged agent message.
func (m *Model) listen() tea.Cmd {
	ch := m.msgCh
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		return <-ch
	}
}

func (m *Model) interrupt() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.sessionID != "" && m.ag != nil {
		m.ag.Interrupt(m.sessionID)
	}
	m.setStatus("interrupted")
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
		m.appendTo(blockReasoning, e.Delta)
	case agent.EventToolCall:
		m.closeStreaming()
		m.blocks = append(m.blocks, block{kind: blockTool, title: toolHeadline(e.Name, e.Arguments), streaming: true})
	case agent.EventToolProgress:
		m.appendTo(blockTool, e.Chunk)
	case agent.EventToolResult:
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].kind == blockTool && !m.blocks[i].done {
				m.blocks[i].text = e.Content
				m.blocks[i].done = true
				m.blocks[i].isError = e.IsError
				m.blocks[i].streaming = false
				break
			}
		}
	case agent.EventNotice:
		m.blocks = append(m.blocks, block{kind: blockNotice, text: e.Message})
	case agent.EventError:
		m.blocks = append(m.blocks, block{kind: blockError, text: e.Err})
	case agent.EventUsage:
		m.tokensIn, m.tokensOut = e.InputTokens, e.OutputTokens
	}
}

func (m *Model) appendTo(kind blockKind, text string) {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == kind && m.blocks[n-1].streaming {
		m.blocks[n-1].text += text
		return
	}
	m.blocks = append(m.blocks, block{kind: kind, text: text, streaming: true})
}

func (m *Model) closeStreaming() {
	for i := range m.blocks {
		m.blocks[i].streaming = false
	}
}

func (m *Model) setStatus(s string) { m.status = s }

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
