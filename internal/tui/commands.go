package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/rag"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

// Command is one slash command.
type Command struct {
	Name    string
	Summary string
	// Run reports whether the TUI should exit.
	Run func(m *Model, ctx context.Context, args string) bool
}

// commands is the registry, kept sorted for the palette.
var commands []Command

func init() {
	commands = []Command{
		{"help", "Show every command", cmdHelp},
		{"new", "Start a fresh session", cmdNew},
		{"sessions", "List recent sessions", cmdSessions},
		{"resume", "Resume a session by id", cmdResume},
		{"model", "Show or change the active model", cmdModel},
		{"models", "List models the provider offers", cmdModels},
		{"provider", "Show or change the provider", cmdProvider},
		{"tools", "List the tools available this turn", cmdTools},
		{"toolset", "Switch the active toolset", cmdToolset},
		{"skills", "List stored skills", cmdSkills},
		{"memory", "Search or list long-term memory", cmdMemory},
		{"rag", "Show retrieval status", cmdRAG},
		{"mcp", "Show MCP servers", cmdMCP},
		{"status", "Runtime and storage summary", cmdStatus},
		{"config", "Read or set a config value", cmdConfig},
		{"setup", "Open the setup wizard", cmdSetup},
		{"reasoning", "Toggle reasoning display", cmdReasoning},
		{"compact", "Summarise this session now", cmdCompact},
		{"clear", "Clear the transcript", cmdClear},
		{"stop", "Interrupt the current turn", cmdStop},
		{"retry", "Resend the last message", cmdRetry},
		{"copy", "Copy the last reply to the clipboard", cmdCopy},
		{"web", "Print the dashboard URL", cmdWeb},
		{"version", "Show the version", cmdVersion},
		{"quit", "Leave the TUI", func(*Model, context.Context, string) bool { return true }},
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
}

// updatePalette recomputes slash completions from the current buffer.
func (m *Model) updatePalette() {
	text := m.ed.text()
	// Only complete when the buffer is a single line starting with "/".
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		m.palette = nil
		return
	}
	word := strings.TrimPrefix(text, "/")
	if i := strings.IndexByte(word, ' '); i >= 0 {
		m.palette = nil // arguments are being typed; stop suggesting
		return
	}

	var matches []Command
	for _, c := range commands {
		if strings.HasPrefix(c.Name, strings.ToLower(word)) {
			matches = append(matches, c)
		}
	}
	m.palette = matches
	if m.paletteSel >= len(matches) {
		m.paletteSel = 0
	}
}

// acceptCompletion replaces the buffer with the highlighted command.
func (m *Model) acceptCompletion() {
	if len(m.palette) == 0 {
		return
	}
	m.ed.setText("/" + m.palette[m.paletteSel].Name + " ")
	m.palette = nil
	m.paletteSel = 0
}

// commandExists reports whether name is a complete command, which decides
// whether Enter runs it or completes it.
func commandExists(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

// runCommand dispatches a slash command; it reports whether to quit.
func (m *Model) runCommand(ctx context.Context, line string) bool {
	name, args, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	name = strings.ToLower(strings.TrimSpace(name))
	args = strings.TrimSpace(args)

	for _, c := range commands {
		if c.Name == name {
			return c.Run(m, ctx, args)
		}
	}
	m.push(block{
		kind: blockError,
		text: fmt.Sprintf("Unknown command /%s — try /help", name),
	})
	return false
}

// ---- command implementations -------------------------------------------------

func cmdHelp(m *Model, _ context.Context, _ string) bool {
	var b strings.Builder
	b.WriteString("Commands\n\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  /%-12s %s\n", c.Name, c.Summary)
	}
	b.WriteString("\nKeys\n\n")
	b.WriteString("  Enter          send\n")
	b.WriteString("  Alt+Enter      newline\n")
	b.WriteString("  Up / Down      recall history\n")
	b.WriteString("  PgUp / PgDn    scroll the transcript\n")
	b.WriteString("  Tab            complete a command\n")
	b.WriteString("  Ctrl+C         interrupt, then quit\n")
	b.WriteString("  Ctrl+L         clear the transcript\n")
	b.WriteString("  Ctrl+W         delete the previous word\n")
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdNew(m *Model, _ context.Context, _ string) bool {
	m.sessionID = ""
	m.title = ""
	m.mu.Lock()
	m.blocks = nil
	m.mu.Unlock()
	m.greet()
	m.setStatus("started a new session")
	return false
}

func cmdSessions(m *Model, ctx context.Context, _ string) bool {
	list, _, err := m.db.ListSessions(ctx, store.SessionFilter{Limit: 15})
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	if len(list) == 0 {
		m.push(block{kind: blockSystem, text: "No sessions yet."})
		return false
	}
	var b strings.Builder
	b.WriteString("Recent sessions\n\n")
	for _, s := range list {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "  %-14s  %-40s  %d msg  %s\n",
			s.ID[:min(14, len(s.ID))], truncateVisible(title, 40), s.MessageCount,
			s.UpdatedAt.Format("02 Jan 15:04"))
	}
	b.WriteString("\nResume one with /resume <id>")
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdResume(m *Model, ctx context.Context, args string) bool {
	if args == "" {
		m.push(block{kind: blockNotice, text: "Usage: /resume <session id>"})
		return false
	}
	// Accept a prefix so users can paste the shortened id from /sessions.
	list, _, err := m.db.ListSessions(ctx, store.SessionFilter{Limit: 200})
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	var found *store.Session
	for i := range list {
		if strings.HasPrefix(list[i].ID, args) {
			found = &list[i]
			break
		}
	}
	if found == nil {
		m.push(block{kind: blockError, text: "No session matches " + args})
		return false
	}

	messages, err := m.db.ListMessages(ctx, found.ID, 0, 0)
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	m.sessionID = found.ID
	m.title = found.Title
	m.mu.Lock()
	m.blocks = nil
	for _, msg := range messages {
		switch msg.Role {
		case store.RoleUser:
			m.blocks = append(m.blocks, block{kind: blockUser, text: msg.Content})
		case store.RoleAssistant:
			if msg.Content != "" {
				m.blocks = append(m.blocks, block{kind: blockAssistant, text: msg.Content})
			}
		case store.RoleTool:
			m.blocks = append(m.blocks, block{
				kind: blockTool, title: msg.ToolName, text: msg.Content, done: true,
			})
		}
	}
	m.mu.Unlock()
	m.setStatus("resumed %s", found.ID[:min(12, len(found.ID))])
	return false
}

func cmdModel(m *Model, _ context.Context, args string) bool {
	if args == "" {
		m.push(block{kind: blockSystem, text: fmt.Sprintf(
			"Model: %s\nProvider: %s\n\nChange it with /model <id>",
			orDash(m.cfg.Model.Default), orDash(m.cfg.Model.Provider))})
		return false
	}
	cfg, err := config.Reload()
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	cfg.Model.Default = args
	if err := config.Save(cfg); err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	m.cfg = cfg
	m.agent.SetConfig(cfg)
	m.setStatus("model set to %s", args)
	return false
}

func cmdModels(m *Model, ctx context.Context, args string) bool {
	provider := args
	if provider == "" {
		provider = m.cfg.Model.Provider
	}
	m.push(block{kind: blockSystem, text: "Fetching models from " + provider + "…"})
	m.render()

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	models, err := m.agent.Models(listCtx, provider)
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d model(s) from %s\n\n", len(models), provider)
	for i, mo := range models {
		if i >= 40 {
			fmt.Fprintf(&b, "  … and %d more\n", len(models)-40)
			break
		}
		fmt.Fprintf(&b, "  %s\n", mo.ID)
	}
	b.WriteString("\nSelect one with /model <id>")
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdProvider(m *Model, _ context.Context, args string) bool {
	if args == "" {
		var names []string
		for name := range m.cfg.Providers {
			names = append(names, name)
		}
		sort.Strings(names)
		var b strings.Builder
		fmt.Fprintf(&b, "Active provider: %s\n\nConfigured:\n", orDash(m.cfg.Model.Provider))
		for _, n := range names {
			p := m.cfg.Providers[n]
			mark := " "
			if n == m.cfg.Model.Provider {
				mark = "•"
			}
			key := "no key"
			if p.APIKey != "" {
				key = "key set"
			}
			fmt.Fprintf(&b, "  %s %-14s %-20s %s\n", mark, n, p.Kind, key)
		}
		b.WriteString("\nSwitch with /provider <name>")
		m.push(block{kind: blockSystem, text: b.String()})
		return false
	}
	cfg, err := config.Reload()
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	if _, ok := cfg.Providers[args]; !ok {
		m.push(block{kind: blockError, text: "Unknown provider " + args})
		return false
	}
	cfg.Model.Provider = args
	if err := config.Save(cfg); err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	m.cfg = cfg
	m.agent.SetConfig(cfg)
	m.setStatus("provider set to %s", args)
	return false
}

func cmdTools(m *Model, _ context.Context, _ string) bool {
	names := m.agent.Registry().Resolve(
		m.cfg.Tools.Toolset, m.cfg.Tools.Enabled, m.cfg.Tools.Disabled)
	var b strings.Builder
	fmt.Fprintf(&b, "Toolset %q — %d tool(s)\n\n", m.cfg.Tools.Toolset, len(names))
	for _, t := range names {
		fmt.Fprintf(&b, "  %-16s %s\n", t.Name(), truncateVisible(t.Description(), max(20, m.width-24)))
	}
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdToolset(m *Model, _ context.Context, args string) bool {
	if args == "" {
		m.push(block{kind: blockSystem, text: "Toolsets: minimal, coding, research, default, all\n\nSwitch with /toolset <name>"})
		return false
	}
	cfg, err := config.Reload()
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	cfg.Tools.Toolset = args
	cfg.Tools.Enabled, cfg.Tools.Disabled = nil, nil
	if err := config.Save(cfg); err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	m.cfg = cfg
	m.agent.SetConfig(cfg)
	m.setStatus("toolset set to %s", args)
	return false
}

func cmdSkills(m *Model, _ context.Context, _ string) bool {
	lib := m.agent.Skills()
	if lib == nil {
		m.push(block{kind: blockNotice, text: "Skills are disabled."})
		return false
	}
	list := lib.List()
	if len(list) == 0 {
		m.push(block{kind: blockSystem, text: "No skills stored yet. Antares writes them itself after solving something reusable."})
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s)\n\n", len(list))
	for _, s := range list {
		mark := " "
		if !s.Enabled {
			mark = "×"
		}
		fmt.Fprintf(&b, "  %s %-22s %s\n", mark, s.Name, truncateVisible(s.Description, max(20, m.width-30)))
	}
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdMemory(m *Model, ctx context.Context, args string) bool {
	var (
		items []store.Memory
		err   error
	)
	if args == "" {
		items, err = m.db.ListMemories(ctx, "", "", 25)
	} else {
		items, err = m.db.SearchMemories(ctx, args, 25)
	}
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	if len(items) == 0 {
		m.push(block{kind: blockSystem, text: "No memories stored."})
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d memory item(s)\n\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "  [%s] %s\n    %s\n", it.Scope, it.Key, it.Content)
	}
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdRAG(m *Model, ctx context.Context, _ string) bool {
	st := rag.Describe(ctx, m.cfg, m.agent.RAG())
	var b strings.Builder
	fmt.Fprintf(&b, "RAG: %s\n", map[bool]string{true: "enabled", false: "disabled"}[st.Enabled])
	if st.Provider != "" {
		fmt.Fprintf(&b, "Provider: %s (%s)\n", st.Provider,
			map[bool]string{true: "reachable", false: "unreachable"}[st.Reachable])
	}
	if st.Detail != "" {
		fmt.Fprintf(&b, "%s\n", st.Detail)
	}
	if len(st.Collections) > 0 {
		fmt.Fprintf(&b, "Collections: %s\n", strings.Join(st.Collections, ", "))
	}
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdMCP(m *Model, _ context.Context, _ string) bool {
	if !m.cfg.MCP.Enabled || len(m.cfg.MCP.Servers) == 0 {
		m.push(block{kind: blockSystem, text: "No MCP servers configured."})
		return false
	}
	var b strings.Builder
	b.WriteString("MCP servers\n\n")
	names := make([]string, 0, len(m.cfg.MCP.Servers))
	for n := range m.cfg.MCP.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sc := m.cfg.MCP.Servers[n]
		state := "disabled"
		if sc.Enabled {
			state = sc.Transport
			if state == "" {
				state = "stdio"
			}
		}
		fmt.Fprintf(&b, "  %-16s %s\n", n, state)
	}
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdStatus(m *Model, ctx context.Context, _ string) bool {
	st, err := m.db.Stats(ctx)
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n\n", version.Display, version.Version)
	fmt.Fprintf(&b, "  Model      %s (%s)\n", orDash(m.cfg.Model.Default), orDash(m.cfg.Model.Provider))
	fmt.Fprintf(&b, "  Workspace  %s\n", m.cfg.Agent.Workspace)
	fmt.Fprintf(&b, "  Database   %s · %d sessions, %d messages\n", m.db.Driver(), st.Sessions, st.Messages)
	fmt.Fprintf(&b, "  Memories   %d\n", st.Memories)
	fmt.Fprintf(&b, "  Tokens     %d in / %d out\n", st.TokensIn, st.TokensOut)
	fmt.Fprintf(&b, "  Config     %s\n", config.ConfigFile())
	m.push(block{kind: blockSystem, text: b.String()})
	return false
}

func cmdConfig(m *Model, _ context.Context, args string) bool {
	if args == "" {
		m.push(block{kind: blockNotice, text: "Usage: /config <path> [value]   e.g. /config agent.max_turns 50"})
		return false
	}
	path, value, hasValue := strings.Cut(args, " ")
	cfg, err := config.Reload()
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	if !hasValue {
		v, err := cfg.GetPath(path)
		if err != nil {
			m.push(block{kind: blockError, text: err.Error()})
			return false
		}
		m.push(block{kind: blockSystem, text: fmt.Sprintf("%s = %v", path, v)})
		return false
	}
	if err := cfg.SetPath(path, strings.TrimSpace(value)); err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	if err := config.Save(cfg); err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	m.cfg = cfg
	m.agent.SetConfig(cfg)
	m.setStatus("%s = %s", path, strings.TrimSpace(value))
	return false
}

func cmdSetup(m *Model, _ context.Context, _ string) bool {
	m.push(block{kind: blockNotice, text: "Leave the TUI and run `antares setup` to reconfigure."})
	return false
}

func cmdReasoning(m *Model, _ context.Context, _ string) bool {
	cfg, err := config.Reload()
	if err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	cfg.Display.ShowReasoning = !cfg.Display.ShowReasoning
	if err := config.Save(cfg); err != nil {
		m.push(block{kind: blockError, text: err.Error()})
		return false
	}
	m.cfg = cfg
	m.agent.SetConfig(cfg)
	m.setStatus("reasoning display %s", map[bool]string{true: "on", false: "off"}[cfg.Display.ShowReasoning])
	return false
}

func cmdCompact(m *Model, _ context.Context, _ string) bool {
	m.push(block{kind: blockNotice, text: "Context is compacted automatically as the window fills; there is nothing to do by hand."})
	return false
}

func cmdClear(m *Model, _ context.Context, _ string) bool {
	m.mu.Lock()
	m.blocks = nil
	m.mu.Unlock()
	m.scroll = 0
	m.greet()
	return false
}

func cmdStop(m *Model, _ context.Context, _ string) bool {
	if !m.busy {
		m.setStatus("nothing is running")
		return false
	}
	m.interrupt()
	return false
}

func cmdRetry(m *Model, ctx context.Context, _ string) bool {
	if len(m.ed.history) == 0 {
		m.setStatus("nothing to retry")
		return false
	}
	m.send(ctx, m.ed.history[len(m.ed.history)-1])
	return false
}

func cmdCopy(m *Model, _ context.Context, _ string) bool {
	m.mu.Lock()
	text := ""
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockAssistant {
			text = m.blocks[i].text
			break
		}
	}
	m.mu.Unlock()
	if text == "" {
		m.setStatus("no reply to copy")
		return false
	}
	// OSC 52 asks the terminal itself to set the clipboard, which works over SSH.
	fmt.Fprintf(m.out, "\x1b]52;c;%s\x07", base64Encode(text))
	m.setStatus("copied %d characters", len(text))
	return false
}

func cmdWeb(m *Model, _ context.Context, _ string) bool {
	host := m.cfg.Server.Host
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	m.push(block{kind: blockSystem, text: fmt.Sprintf("Dashboard: http://%s:%d", host, m.cfg.Server.Port)})
	return false
}

func cmdVersion(m *Model, _ context.Context, _ string) bool {
	m.push(block{kind: blockSystem, text: fmt.Sprintf("%s %s (commit %s, built %s)",
		version.Display, version.Version, version.Commit, version.Date)})
	return false
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
