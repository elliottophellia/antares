package agent

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
	"github.com/enowdev/antares/internal/version"
)

// buildSystemPrompt assembles identity, environment, memory, and tool guidance.
func (a *Agent) buildSystemPrompt(ctx context.Context, req Request, sess *store.Session, active []tools.Tool) string {
	cfg := a.cfg
	var b strings.Builder

	b.WriteString("You are ")
	b.WriteString(version.Display)
	b.WriteString(", an autonomous AI agent that works on the user's behalf on their own machine.\n\n")

	b.WriteString(`## How you work

- Act on the request as asked. Do the whole task, not just the easy part.
- Prefer using your tools to find out rather than guessing or asking. Read the file, run the command, search the web.
- When a task needs several steps, keep a task list with the todo tool so progress stays visible.
- Report outcomes honestly. If a command failed, say so and show the output. Never claim work you did not verify.
- Be concise. Skip preamble and restating the question; lead with the answer or the result.
- Match the user's language. If they write Indonesian, answer in Indonesian.
`)

	// A standing goal is the whole frame for the turn, so it goes near the top.
	if g, ok := a.GetGoal(ctx, sess.ID); ok && !g.Done && !g.Paused {
		b.WriteString("\n## Standing goal\n\n")
		b.WriteString("You are working towards this until it is met. Every turn should move it forward:\n\n")
		b.WriteString(g.Text)
		b.WriteString("\n")
	}

	b.WriteString("\n## Environment\n\n")
	fmt.Fprintf(&b, "- Date: %s\n", time.Now().Format("Monday, 2 January 2006, 15:04 MST"))
	fmt.Fprintf(&b, "- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "- Workspace: %s\n", sess.Workspace)
	if host, err := os.Hostname(); err == nil {
		fmt.Fprintf(&b, "- Host: %s\n", host)
	}
	fmt.Fprintf(&b, "- Channel: %s\n", firstNonEmpty(req.Platform, "web"))
	fmt.Fprintf(&b, "- Terminal backend: %s\n", cfg.Terminal.Backend)

	if len(active) > 0 {
		b.WriteString("\n## Tool notes\n\n")
		b.WriteString("- Paths given to file tools are relative to the workspace; you cannot read outside it.\n")
		b.WriteString("- The terminal keeps state between calls: `cd`, exports, and activated environments persist.\n")
		if hasTool(active, "memory") && cfg.Memory.Enabled {
			b.WriteString("- Save durable facts about the user or project with the memory tool. Save only what stays true across sessions.\n")
		}
		if hasTool(active, "rag_search") && a.rag != nil {
			b.WriteString("- Before reading many files, try rag_search to locate the relevant passages first.\n")
		}
		if hasTool(active, "delegate_task") && cfg.Delegation.Enabled {
			b.WriteString("- Delegate self-contained research or parallel work with delegate_task; the sub-agent cannot see this conversation, so its prompt must stand alone.\n")
		}
		if hasTool(active, "http_request") {
			b.WriteString("- For HTTP requests and API calls, use http_request rather than curl or wget in the terminal: it presents a real browser's TLS and HTTP/2 fingerprint, so endpoints behind bot-detection answer instead of refusing at the handshake.\n")
		}
	}

	if a.skills != nil && cfg.Skills.Enabled {
		if catalogue := a.skills.PromptBlock(60); catalogue != "" {
			b.WriteString("\n## Your skills\n\n")
			b.WriteString("Procedures you have learned. Read one with the skill tool before following it.\n\n")
			b.WriteString(catalogue)
		}
	}

	if cfg.Memory.Enabled {
		if mem := a.memoryBlock(ctx, req, sess); mem != "" {
			b.WriteString("\n## What you remember\n\n")
			b.WriteString(mem)
		}
	}

	if s := strings.TrimSpace(cfg.Agent.SystemPromptExtra); s != "" {
		b.WriteString("\n## Additional instructions\n\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(req.SystemExtra); s != "" {
		b.WriteString("\n## Task context\n\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if p := strings.TrimSpace(cfg.Agent.Personality); p != "" && p != "default" {
		fmt.Fprintf(&b, "\n## Persona\n\n%s\n", p)
	}

	return b.String()
}

// memoryBlock renders stored memories, bounded by the configured char limit.
func (a *Agent) memoryBlock(ctx context.Context, req Request, sess *store.Session) string {
	limit := a.cfg.Memory.MemoryCharLimit
	if limit <= 0 {
		limit = 12000
	}
	var lines []string
	used := 0

	add := func(items []store.Memory) {
		for _, m := range items {
			line := "- " + m.Content
			if used+len(line) > limit {
				return
			}
			lines = append(lines, line)
			used += len(line)
		}
	}

	if global, err := a.db.ListMemories(ctx, "global", "", 80); err == nil {
		add(global)
	}
	if req.UserID != "" {
		if user, err := a.db.ListMemories(ctx, "user", req.UserID, 40); err == nil {
			add(user)
		}
	}
	if sessMem, err := a.db.ListMemories(ctx, "session", sess.ID, 20); err == nil {
		add(sessMem)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func hasTool(list []tools.Tool, name string) bool {
	for _, t := range list {
		if t.Name() == name {
			return true
		}
	}
	return false
}

// resolveTools picks the tool set for this run, honouring platform overrides.
func (a *Agent) resolveTools(req Request) []tools.Tool {
	cfg := a.cfg
	toolset := req.Toolset
	if toolset == "" {
		if v, ok := cfg.Tools.Platform[req.Platform]; ok && v != "" {
			toolset = v
		}
	}
	if toolset == "" {
		toolset = cfg.Tools.Toolset
	}

	selected := a.reg.Resolve(toolset, cfg.Tools.Enabled, cfg.Tools.Disabled)

	// Drop tools whose backing service is unavailable so the model is not shown
	// capabilities that will always fail.
	out := make([]tools.Tool, 0, len(selected))
	for _, t := range selected {
		switch t.Name() {
		case "rag_search", "rag_index":
			if a.rag == nil {
				continue
			}
		case "memory":
			if !cfg.Memory.Enabled {
				continue
			}
		case "delegate_task":
			if !cfg.Delegation.Enabled || req.Depth > 0 {
				continue
			}
		case "web_search":
			if strings.EqualFold(cfg.Tools.WebSearch.Provider, "none") {
				continue
			}
		case "skill":
			if a.skills == nil || !cfg.Skills.Enabled {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// estimateTokens is a cheap heuristic (~4 chars per token) used for compaction
// decisions; exact counts are not needed to decide when to summarise.
func estimateTokens(msgs []llm.Message, system string) int {
	total := len(system) / 4
	for _, m := range msgs {
		total += len(m.Content)/4 + len(m.Reasoning)/4 + 8
		for _, tc := range m.ToolCalls {
			total += len(tc.Arguments)/4 + len(tc.Name)/4 + 8
		}
		for _, p := range m.Parts {
			if p.Type == "image" {
				total += 800 // rough per-image cost
				continue
			}
			total += len(p.Text) / 4
		}
	}
	return total
}
