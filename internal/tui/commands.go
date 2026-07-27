package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

// Command is one slash command.
type Command struct {
	Name    string
	Summary string
	Run     func(m *Model, args string) (quit bool, cmd tea.Cmd)
}

var commands []Command

func init() {
	commands = []Command{
		{"help", "Show every command", (*Model).cmdHelp},
		{"new", "Start a fresh session", (*Model).cmdNew},
		{"sessions", "List recent sessions", (*Model).cmdSessions},
		{"resume", "Resume a session by id", (*Model).cmdResume},
		{"model", "Show or set the active model", (*Model).cmdModel},
		{"theme", "Show or set the colour theme", (*Model).cmdTheme},
		{"reasoning", "Toggle reasoning display", (*Model).cmdReasoning},
		{"clear", "Clear the transcript", (*Model).cmdClear},
		{"stop", "Interrupt the current turn", (*Model).cmdStop},
		{"status", "Runtime summary", (*Model).cmdStatus},
		{"web", "Print the dashboard URL", (*Model).cmdWeb},
		{"version", "Show the version", (*Model).cmdVersion},
		{"quit", "Leave the TUI", func(*Model, string) (bool, tea.Cmd) { return true, nil }},
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
}

func commandExists(name string) bool {
	for _, c := range commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

// updatePalette recomputes the command suggestions from the current input.
func (m *Model) updatePalette() {
	text := m.ta.Value()
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \n") {
		m.palette = nil
		return
	}
	prefix := strings.TrimPrefix(text, "/")
	var out []Command
	for _, c := range commands {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
		}
	}
	m.palette = out
	if m.paletteSel >= len(out) {
		m.paletteSel = 0
	}
}

func (m *Model) acceptCompletion() {
	if len(m.palette) == 0 {
		return
	}
	c := m.palette[m.paletteSel]
	m.ta.SetValue("/" + c.Name + " ")
	m.ta.CursorEnd()
	m.palette = nil
}

// runCommand parses and runs a slash command.
func (m *Model) runCommand(line string) (bool, tea.Cmd) {
	line = strings.TrimSpace(strings.TrimPrefix(line, "/"))
	name, args, _ := strings.Cut(line, " ")
	args = strings.TrimSpace(args)
	for _, c := range commands {
		if c.Name == name {
			return c.Run(m, args)
		}
	}
	m.pushSystem("Unknown command /" + name + ". Try /help.")
	return false, nil
}

func (m *Model) pushSystem(text string) {
	m.blocks = append(m.blocks, block{kind: blockSystem, text: text})
}

// ---- command implementations ------------------------------------------------

func (m *Model) cmdHelp(string) (bool, tea.Cmd) {
	var b strings.Builder
	b.WriteString("Commands:\n")
	for _, c := range commands {
		b.WriteString(fmt.Sprintf("  /%-10s %s\n", c.Name, c.Summary))
	}
	m.pushSystem(strings.TrimRight(b.String(), "\n"))
	return false, nil
}

func (m *Model) cmdNew(string) (bool, tea.Cmd) {
	m.sessionID = ""
	m.title = ""
	m.tokensIn, m.tokensOut = 0, 0
	m.blocks = nil
	m.greet()
	m.setStatus("new session")
	return false, nil
}

func (m *Model) cmdClear(string) (bool, tea.Cmd) {
	m.blocks = nil
	m.greet()
	return false, nil
}

func (m *Model) cmdTheme(args string) (bool, tea.Cmd) {
	if args == "" {
		var b strings.Builder
		b.WriteString("Themes (use /theme <name>):\n")
		for _, n := range themeNames() {
			mark := "  "
			if n == m.themeName {
				mark = "❯ "
			}
			b.WriteString(mark + n + "\n")
		}
		m.pushSystem(strings.TrimRight(b.String(), "\n"))
		return false, nil
	}
	if _, ok := themes[args]; !ok {
		m.pushSystem("Unknown theme " + args + ". Try /theme to list.")
		return false, nil
	}
	m.themeName = args
	m.st = newStyles(themeByName(args))
	m.cache = nil
	if m.cfg != nil {
		m.cfg.Display.Theme = args
		if m.ag != nil {
			m.ag.SetConfig(m.cfg)
		}
	}
	m.setStatus("theme → " + args)
	return false, nil
}

func (m *Model) cmdReasoning(string) (bool, tea.Cmd) {
	m.showReasoning = !m.showReasoning
	m.setStatus("reasoning " + onOff(m.showReasoning))
	return false, nil
}

func (m *Model) cmdStop(string) (bool, tea.Cmd) {
	if m.busy {
		m.interrupt()
	} else {
		m.setStatus("nothing running")
	}
	return false, nil
}

func (m *Model) cmdVersion(string) (bool, tea.Cmd) {
	m.pushSystem(version.Display + " " + version.Version)
	return false, nil
}

func (m *Model) cmdWeb(string) (bool, tea.Cmd) {
	port := 8787
	if m.cfg != nil && m.cfg.Server.Port > 0 {
		port = m.cfg.Server.Port
	}
	m.pushSystem(fmt.Sprintf("Dashboard: http://localhost:%d", port))
	return false, nil
}

func (m *Model) cmdModel(args string) (bool, tea.Cmd) {
	if args == "" {
		model, provider := "—", ""
		if m.cfg != nil {
			model, provider = m.cfg.Model.Default, m.cfg.Model.Provider
		}
		m.pushSystem(fmt.Sprintf("Model: %s · %s (use /model <name> to change)", model, provider))
		return false, nil
	}
	if m.cfg == nil {
		return false, nil
	}
	m.cfg.Model.Default = args
	if m.ag != nil {
		m.ag.SetConfig(m.cfg)
	}
	m.setStatus("model → " + args)
	m.pushSystem("Model set to " + args)
	return false, nil
}

func (m *Model) cmdStatus(string) (bool, tea.Cmd) {
	model, provider := "—", ""
	if m.cfg != nil {
		model, provider = m.cfg.Model.Default, m.cfg.Model.Provider
	}
	sess := m.sessionID
	if sess == "" {
		sess = "(new)"
	}
	m.pushSystem(fmt.Sprintf("Model: %s · %s\nSession: %s\nTokens: %d in / %d out\nReasoning: %s",
		model, provider, sess, m.tokensIn, m.tokensOut, onOff(m.showReasoning)))
	return false, nil
}

func (m *Model) cmdSessions(string) (bool, tea.Cmd) {
	if m.db == nil {
		m.pushSystem("No store available.")
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, _, err := m.db.ListSessions(ctx, store.SessionFilter{Limit: 15})
	if err != nil {
		m.pushSystem("Could not list sessions: " + err.Error())
		return false, nil
	}
	if len(list) == 0 {
		m.pushSystem("No saved sessions yet.")
		return false, nil
	}
	var b strings.Builder
	b.WriteString("Recent sessions (use /resume <id>):\n")
	for _, s := range list {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		b.WriteString(fmt.Sprintf("  %s  %s  (%d msgs)\n", shortID(s.ID), truncate(title, 40), s.MessageCount))
	}
	m.pushSystem(strings.TrimRight(b.String(), "\n"))
	return false, nil
}

func (m *Model) cmdResume(args string) (bool, tea.Cmd) {
	if args == "" {
		m.pushSystem("Usage: /resume <id> — see /sessions for ids.")
		return false, nil
	}
	if m.db == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, _, err := m.db.ListSessions(ctx, store.SessionFilter{Limit: 200})
	if err != nil {
		m.pushSystem("Could not load sessions: " + err.Error())
		return false, nil
	}
	var found *store.Session
	for i := range list {
		if strings.HasPrefix(list[i].ID, args) {
			found = &list[i]
			break
		}
	}
	if found == nil {
		m.pushSystem("No session matching " + args)
		return false, nil
	}
	msgs, err := m.db.ListMessages(ctx, found.ID, 0, 0)
	if err != nil {
		m.pushSystem("Could not load messages: " + err.Error())
		return false, nil
	}
	m.sessionID = found.ID
	m.title = found.Title
	m.blocks = nil
	for _, msg := range msgs {
		switch msg.Role {
		case store.RoleUser:
			m.blocks = append(m.blocks, block{kind: blockUser, text: msg.Content})
		case store.RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				m.blocks = append(m.blocks, block{kind: blockAssistant, text: msg.Content, done: true})
			}
		case store.RoleTool:
			m.blocks = append(m.blocks, block{kind: blockTool, title: msg.ToolName, text: msg.Content, done: true})
		}
	}
	m.setStatus("resumed " + shortID(found.ID))
	return false, nil
}

// ---- tool headline ----------------------------------------------------------

func toolHeadline(name, args string) string {
	summary := summarizeArgs(args)
	if summary == "" {
		return name
	}
	return name + "  " + summary
}

// summarizeArgs renders a one-line hint from a tool's JSON arguments.
func summarizeArgs(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	for _, k := range []string{"path", "command", "query", "pattern", "url", "target", "domain", "ip", "action"} {
		if v, ok := m[k]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}
