package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
)

// resolveSession loads the requested session or creates a fresh one.
func (a *Agent) resolveSession(ctx context.Context, req *Request) (*store.Session, error) {
	if req.SessionID != "" {
		sess, err := a.db.GetSession(ctx, req.SessionID)
		if err == nil {
			return sess, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	platform := req.Platform
	if platform == "" {
		platform = "web"
	}
	sess := &store.Session{
		ID:        newID("ses"),
		Title:     defaultTitle(req.Message),
		Platform:  platform,
		ChannelID: req.ChannelID,
		UserID:    req.UserID,
		Model:     firstNonEmpty(req.Model, a.cfg.Model.Default),
		Provider:  a.cfg.Model.Provider,
		Workspace: a.cfg.Agent.Workspace,
		Meta:      store.Meta{},
	}
	if req.Quiet {
		// Sub-agent runs are ephemeral: keep the session in memory only.
		req.SessionID = sess.ID
		return sess, nil
	}
	if err := a.db.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	req.SessionID = sess.ID
	return sess, nil
}

func defaultTitle(msg string) string {
	msg = strings.TrimSpace(strings.ReplaceAll(msg, "\n", " "))
	if msg == "" {
		return "Percakapan baru"
	}
	runes := []rune(msg)
	if len(runes) > 60 {
		return string(runes[:60]) + "…"
	}
	return msg
}

// loadHistory rebuilds the model-facing message list from storage.
func (a *Agent) loadHistory(ctx context.Context, sessionID string, req Request) ([]llm.Message, error) {
	if req.Quiet {
		return nil, nil
	}
	rows, err := a.db.ListMessages(ctx, sessionID, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Message, 0, len(rows))
	for _, r := range rows {
		if r.Hidden {
			continue
		}
		m := llm.Message{Content: r.Content, Reasoning: r.Reasoning}
		switch r.Role {
		case store.RoleUser:
			m.Role = llm.RoleUser
			if r.Attachments != "" {
				var parts []llm.Part
				if err := json.Unmarshal([]byte(r.Attachments), &parts); err == nil {
					m.Parts = parts
				}
			}
		case store.RoleAssistant:
			m.Role = llm.RoleAssistant
			if r.ToolCalls != "" {
				var calls []llm.ToolCall
				if err := json.Unmarshal([]byte(r.ToolCalls), &calls); err == nil {
					m.ToolCalls = calls
				}
			}
		case store.RoleTool:
			m.Role = llm.RoleTool
			m.ToolCallID = r.ToolCallID
			m.Name = r.ToolName
		default:
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// persistAssistant stores an assistant turn including any tool calls.
func (a *Agent) persistAssistant(ctx context.Context, sessionID, model string, resp *llm.Response) {
	toolCalls := ""
	if len(resp.ToolCalls) > 0 {
		if b, err := json.Marshal(resp.ToolCalls); err == nil {
			toolCalls = string(b)
		}
	}
	if resp.Content == "" && toolCalls == "" && resp.Reasoning == "" {
		return
	}
	if err := a.db.AppendMessage(ctx, &store.Message{
		ID: newID("msg"), SessionID: sessionID, Role: store.RoleAssistant,
		Content: resp.Content, Reasoning: resp.Reasoning, ToolCalls: toolCalls,
		Model: model, TokensIn: resp.Usage.InputTokens, TokensOut: resp.Usage.OutputTokens,
	}); err != nil {
		slog.Warn("persist assistant message failed", "error", err)
	}
}

func (a *Agent) recordUsage(ctx context.Context, sessionID, provider, model string, u llm.Usage) {
	if err := a.db.RecordUsage(ctx, &store.Usage{
		ID: newID("use"), SessionID: sessionID, Provider: provider, Model: model,
		TokensIn: u.InputTokens, TokensOut: u.OutputTokens, CacheRead: u.CacheReadTokens,
		Cost: estimateCost(model, u), Source: "chat",
	}); err != nil {
		slog.Debug("record usage failed", "error", err)
	}
}

// maybeTitle replaces the placeholder title once a session has real content.
func (a *Agent) maybeTitle(ctx context.Context, sess *store.Session, userMsg, reply string) {
	if sess.Title != "" && sess.Title != "Percakapan baru" && !strings.HasPrefix(sess.Title, userMsg[:minInt(len(userMsg), 20)]) {
		return
	}
	title := summariseTitle(userMsg, reply)
	if title == "" || title == sess.Title {
		return
	}
	sess.Title = title
	if err := a.db.UpdateSession(ctx, sess); err != nil {
		slog.Debug("update session title failed", "error", err)
	}
}

// summariseTitle derives a short label without spending a model call.
func summariseTitle(userMsg, reply string) string {
	base := strings.TrimSpace(strings.ReplaceAll(userMsg, "\n", " "))
	if base == "" {
		base = strings.TrimSpace(strings.ReplaceAll(reply, "\n", " "))
	}
	if base == "" {
		return ""
	}
	// Cut at the first sentence boundary when it lands in a reasonable range.
	for _, sep := range []string{". ", "? ", "! "} {
		if i := strings.Index(base, sep); i > 15 && i < 70 {
			base = base[:i]
			break
		}
	}
	runes := []rune(base)
	if len(runes) > 60 {
		return strings.TrimSpace(string(runes[:60])) + "…"
	}
	return base
}

// estimateCost is a coarse fallback used when the provider omits pricing.
func estimateCost(model string, u llm.Usage) float64 {
	if u.Cost > 0 {
		return u.Cost
	}
	in, out := 0.0, 0.0
	l := strings.ToLower(model)
	switch {
	case strings.Contains(l, "opus"):
		in, out = 15, 75
	case strings.Contains(l, "sonnet"):
		in, out = 3, 15
	case strings.Contains(l, "haiku"):
		in, out = 0.8, 4
	case strings.Contains(l, "gpt-5"), strings.Contains(l, "gpt-4o"):
		in, out = 2.5, 10
	case strings.Contains(l, "gemini") && strings.Contains(l, "pro"):
		in, out = 1.25, 5
	case strings.Contains(l, "gemini"):
		in, out = 0.3, 2.5
	default:
		return 0
	}
	return (float64(u.InputTokens)*in + float64(u.OutputTokens)*out) / 1_000_000
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// touchSession refreshes the updated_at stamp without other changes.
func (a *Agent) touchSession(ctx context.Context, sess *store.Session) {
	sess.UpdatedAt = time.Now()
	_ = a.db.UpdateSession(ctx, sess)
}
