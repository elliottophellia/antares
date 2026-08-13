package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/providers"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/textutil"
)

// contextWindowFor returns the active model's token budget for the usage event.
// Precedence: an explicit per-model model_meta override, then the provider
// catalogue's known window for the model (so e.g. glm-5.2 reads its real 1M
// window instead of the generic default), then the configured window, then a
// sane fallback. It mirrors the window maybeCompact governs, so the UI's
// "context full" bar agrees with compaction.
func (a *Agent) contextWindowFor(model string) int {
	if a.config() != nil {
		for _, p := range a.config().Providers {
			if m, ok := p.ModelMeta[model]; ok && m.ContextWindow > 0 {
				return m.ContextWindow
			}
		}
	}
	if w := providers.ContextWindow(model); w > 0 {
		return w
	}
	if a.config() != nil && a.config().Model.ContextWindow > 0 {
		return a.config().Model.ContextWindow
	}
	return 128000
}

// maybeCompact summarises older turns once the conversation approaches the
// model's context window, keeping recent turns verbatim. On success the
// summary is persisted on the session so the next turn does not re-run a
// multi-minute summarise over thousands of raw messages.
func (a *Agent) maybeCompact(ctx context.Context, history []llm.Message, system, model string, tools []llm.Tool, emit Emit, sess *store.Session) []llm.Message {
	cfg := a.config().Compression
	if !cfg.Enabled || len(history) < 8 {
		return history
	}
	// Use the same window the usage gauge reports (per-model meta, catalogue,
	// then config) so "context full" and compaction agree.
	window := a.contextWindowFor(model)
	if window <= 0 {
		window = 128000
	}
	threshold := cfg.Threshold
	if threshold <= 0 || threshold >= 1 {
		threshold = 0.8
	}

	used := estimateRequestTokens(history, system, tools)
	if float64(used) < float64(window)*threshold {
		// Even below the threshold, prune oversized tool results that are no
		// longer near the tail — they dominate context growth.
		return a.prunedToolResults(history)
	}

	protectFirst := maxInt(cfg.ProtectFirstN, 1)
	protectLast := maxInt(cfg.ProtectLastN, 4)
	head, middle, tail, ok := splitForCompaction(history, protectFirst, protectLast)
	if !ok {
		return history
	}

	// Always surface compaction to the UI: on long sessions this LLM call can
	// take tens of seconds and without a notice the dashboard only shows
	// "Working… · Ns", which looks like a hang.
	if emit != nil {
		_ = emit(Event{Type: EventNotice, Message: fmt.Sprintf(
			"compacting %d older messages to free context (~%d tokens)", len(middle), used)})
	}

	sid := ""
	if sess != nil {
		sid = sess.ID
	}
	summary, _, err := a.summarise(ctx, sid, middle)
	if err != nil {
		slog.Warn("context compaction failed; pruning oversized tool outputs instead of dropping history", "error", err)
		// Fall back to pruning oversized tool results in the middle section so
		// the turn can still proceed without losing all mid-task context.
		// Dropping the entire middle (head+tail only) would silently erase
		// the conversation between protectFirst and protectLast.
		pruned := a.prunedToolResults(middle)
		return append(append([]llm.Message{}, head...), append(pruned, tail...)...)
	}

	compacted := make([]llm.Message, 0, len(head)+len(tail)+1)
	compacted = append(compacted, head...)
	compacted = append(compacted, llm.Message{
		Role: llm.RoleUser,
		Content: "[Compacted summary of the earlier conversation]\n\n" + summary +
			"\n\n[Continue from here. This summary replaces the older messages.]",
	})
	compacted = append(compacted, tail...)

	// Persist so the next turn loads head+summary+tail instead of re-summarising.
	if sess != nil && !isQuietSession(sess) {
		a.persistContextCompact(ctx, sess, summary, protectFirst, protectLast)
	}

	slog.Info("context compacted", "before", len(history), "after", len(compacted), "tokens_before", used)
	return compacted
}

// splitForCompaction divides history into the head kept verbatim, the middle to
// be summarised, and the tail kept verbatim, honouring the tool-call boundary so
// an assistant turn is never split from its results. It reports false when there
// is too little between the protected ends to be worth summarising.
func splitForCompaction(history []llm.Message, protectFirst, protectLast int) (head, middle, tail []llm.Message, ok bool) {
	if len(history) <= protectFirst+protectLast+2 {
		return nil, nil, nil, false
	}
	head = history[:protectFirst]
	middle = history[protectFirst : len(history)-protectLast]
	tail = history[len(history)-protectLast:]
	// Never split an assistant tool-call turn from its tool results, or the
	// provider will reject the request.
	middle, tail = rebalanceToolBoundary(middle, tail)
	if len(middle) == 0 {
		return nil, nil, nil, false
	}
	return head, middle, tail, true
}

// CompactNow summarises a session's older turns immediately, regardless of the
// usage threshold maybeCompact waits for, and streams its progress to emit. It
// is what the /compact command runs: unlike maybeCompact it does not defer the
// work to the next turn, and it finishes by emitting a usage event carrying the
// freed context so the gauge drops the moment compaction lands rather than only
// after the next model call. The summary is persisted, so the next turn loads
// head + summary + tail without re-summarising.
func (a *Agent) CompactNow(ctx context.Context, sessionID string, emit Emit) error {
	if emit == nil {
		emit = func(Event) error { return nil }
	}
	if a.db == nil {
		return fmt.Errorf("no store configured")
	}
	sess, err := a.db.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	cfg := a.config().Compression

	// Resolve the model so the window and the token estimate match the turn the
	// user will run next.
	_, modelName, _, err := a.newClient("", sessionID)
	if err != nil {
		return err
	}

	// Rebuild the request context (role, tools, system prompt) the same way Run
	// does, so the token estimate reflects what the next turn will actually send.
	req := Request{SessionID: sessionID}
	if stored, err := a.db.GetKV(ctx, "role:"+sessionID); err == nil {
		req.Role = stored
	}
	a.applyRole(&req)

	history, err := a.loadHistory(ctx, sess, req)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}

	activeTools := a.resolveTools(req)
	toolSpecs := make([]llm.Tool, 0, len(activeTools))
	for _, t := range activeTools {
		toolSpecs = append(toolSpecs, llm.Tool{Name: t.Name(), Description: t.Description(), Parameters: t.Schema()})
	}
	system := a.buildSystemPrompt(ctx, req, sess, activeTools)

	window := a.contextWindowFor(modelName)
	if window <= 0 {
		window = 128000
	}
	before := estimateRequestTokens(history, system, toolSpecs)

	protectFirst := maxInt(cfg.ProtectFirstN, 1)
	protectLast := maxInt(cfg.ProtectLastN, 4)
	head, middle, tail, ok := splitForCompaction(history, protectFirst, protectLast)
	if !ok {
		_ = emit(Event{Type: EventNotice, Message: "Nothing to compact yet — this conversation is still short."})
		// Report the current fill so the gauge stays accurate even when there
		// was nothing to do.
		_ = emit(Event{Type: EventUsage, ContextTokens: before, ContextWindow: window})
		return nil
	}

	_ = emit(Event{Type: EventNotice, Message: fmt.Sprintf(
		"Compacting %d older messages to free context (~%d tokens in use)…", len(middle), before)})

	summary, sumUsage, err := a.summarise(ctx, sessionID, middle)
	if err != nil {
		return fmt.Errorf("summarise: %w", err)
	}

	if !isQuietSession(sess) {
		a.persistContextCompact(ctx, sess, summary, protectFirst, protectLast)
	}

	compacted := make([]llm.Message, 0, len(head)+1+len(tail))
	compacted = append(compacted, head...)
	compacted = append(compacted, llm.Message{
		Role: llm.RoleUser,
		Content: "[Compacted summary of the earlier conversation]\n\n" + summary +
			"\n\n[Continue from here. This summary replaces the older messages.]",
	})
	compacted = append(compacted, tail...)

	after := estimateRequestTokens(compacted, system, toolSpecs)
	freed := before - after
	if freed < 0 {
		freed = 0
	}
	slog.Info("context compacted on demand", "session", sessionID,
		"before", before, "after", after, "freed", freed,
		"summary_tokens_in", sumUsage.InputTokens, "summary_tokens_out", sumUsage.OutputTokens)

	// Name the cost of the summarising call itself: it is a real provider
	// request, already recorded against the session, and the person asked for
	// it, so it should not be silent.
	costNote := ""
	if sumUsage.InputTokens > 0 || sumUsage.OutputTokens > 0 {
		costNote = fmt.Sprintf(" The summary itself cost ~%d input and %d output tokens.",
			sumUsage.InputTokens, sumUsage.OutputTokens)
	}
	_ = emit(Event{Type: EventNotice, Message: fmt.Sprintf(
		"Compacted %d messages into a summary — freed about %d tokens.%s", len(middle), freed, costNote)})
	// The gauge plots the latest turn's input, so report the estimate for the
	// next turn: the same estimator maybeCompact uses for its own threshold.
	_ = emit(Event{Type: EventUsage, ContextTokens: after, ContextWindow: window})
	return nil
}

// isQuietSession is true for ephemeral sub-agent sessions we never persist.
func isQuietSession(sess *store.Session) bool {
	return sess == nil || sess.ID == ""
}

// persistContextCompact records the summary and the highest seq it covers so
// loadHistory can rebuild the compacted view without another LLM call.
func (a *Agent) persistContextCompact(ctx context.Context, sess *store.Session, summary string, protectFirst, protectLast int) {
	if a.db == nil || sess == nil {
		return
	}
	rows, err := a.db.ListMessages(ctx, sess.ID, 0, 0)
	if err != nil {
		slog.Warn("persist compact: list messages failed", "error", err)
		return
	}
	visible := make([]store.Message, 0, len(rows))
	for _, r := range rows {
		if r.Hidden {
			continue
		}
		visible = append(visible, r)
	}
	if len(visible) <= protectFirst+protectLast {
		return
	}
	// Middle ends at the last message before the protected tail.
	middleEnd := visible[len(visible)-protectLast-1]
	throughSeq := middleEnd.Seq

	if sess.Meta == nil {
		sess.Meta = store.Meta{}
	}
	// Reload session to avoid clobbering concurrent meta updates with a stale
	// struct, then merge our key.
	fresh, err := a.db.GetSession(ctx, sess.ID)
	if err != nil {
		slog.Warn("persist compact: get session failed", "error", err)
		return
	}
	if fresh.Meta == nil {
		fresh.Meta = store.Meta{}
	}
	fresh.Meta[contextCompactMetaKey] = map[string]any{
		"summary":     summary,
		"through_seq": throughSeq,
		"keep_first":  protectFirst,
	}
	if err := a.db.UpdateSession(ctx, fresh); err != nil {
		slog.Warn("persist compact: update session failed", "error", err)
		return
	}
	// Keep the in-memory session in sync for the rest of this turn.
	sess.Meta = fresh.Meta
	slog.Info("context compact persisted", "session", sess.ID, "through_seq", throughSeq, "keep_first", protectFirst)
}

// estimateRequestTokens includes tool schemas as well as system/history. Large
// agent tool packs are sent on every call and can consume a material part of the
// context window; omitting them delays compaction until the provider rejects the
// request even though the history-only estimate still looks safe.
func estimateRequestTokens(history []llm.Message, system string, tools []llm.Tool) int {
	total := estimateTokens(history, system)
	for _, tool := range tools {
		total += len(tool.Name)/4 + len(tool.Description)/4 + 8
		if schema, err := json.Marshal(tool.Parameters); err == nil {
			total += len(schema) / 4
		}
	}
	return total
}

// rebalanceToolBoundary moves messages between middle and tail so that no tool
// result is separated from the assistant turn that requested it.
func rebalanceToolBoundary(middle, tail []llm.Message) ([]llm.Message, []llm.Message) {
	// If the tail starts with tool results, their assistant turn sits at the end
	// of middle: move it across.
	for len(tail) > 0 && tail[0].Role == llm.RoleTool && len(middle) > 0 {
		last := middle[len(middle)-1]
		middle = middle[:len(middle)-1]
		tail = append([]llm.Message{last}, tail...)
		if last.Role == llm.RoleAssistant {
			break
		}
	}
	// If middle now ends with an assistant turn holding unresolved tool calls,
	// drop it too — its results already moved to the tail.
	if n := len(middle); n > 0 && len(middle[n-1].ToolCalls) > 0 {
		middle = middle[:n-1]
	}
	return middle, tail
}

// summarise asks the auxiliary (or main) model to condense a message span. It
// is a real provider call with its own token cost, so that cost is recorded
// against the session (tagged "compaction") and returned to the caller — the
// summary is not free, and the usage totals must say so.
func (a *Agent) summarise(ctx context.Context, sessionID string, msgs []llm.Message) (string, llm.Usage, error) {
	client, model, provider, err := a.newAuxClient(sessionID)
	if err != nil {
		return "", llm.Usage{}, err
	}

	var transcript strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			transcript.WriteString("USER: " + truncate(m.Content, 4000) + "\n\n")
		case llm.RoleAssistant:
			if m.Content != "" {
				transcript.WriteString("ASSISTANT: " + truncate(m.Content, 4000) + "\n")
			}
			for _, tc := range m.ToolCalls {
				transcript.WriteString("ASSISTANT called " + tc.Name + "(" + truncate(tc.Arguments, 400) + ")\n")
			}
			transcript.WriteString("\n")
		case llm.RoleTool:
			transcript.WriteString("TOOL " + m.Name + " → " + truncate(m.Content, 1500) + "\n\n")
		}
	}

	prompt := `Summarise the conversation excerpt below so another instance of the assistant can continue seamlessly.

Preserve, in this order:
1. What the user is trying to achieve, and any constraints or preferences they stated.
2. Decisions made and their rationale.
3. Concrete facts discovered: file paths, commands that worked, error messages, values, URLs.
4. What is done and what is still outstanding.

Drop pleasantries and redundant tool output. Be dense and specific — names and paths, not "a file was read".
Write in the same language the user used.

--- EXCERPT ---
` + transcript.String()

	resp, err := client.Chat(ctx, llm.Request{
		Model:       model,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		MaxTokens:   2048,
		Temperature: 0.2,
	})
	if err != nil {
		return "", llm.Usage{}, err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", llm.Usage{}, fmt.Errorf("summariser returned empty output")
	}
	// A quiet sub-agent session (no id) is never billed to a conversation, but
	// a real one must carry the summary's cost.
	if sessionID != "" {
		a.recordUsageSource(ctx, sessionID, provider, model, resp.Usage, "compaction")
	}
	return resp.Content, resp.Usage, nil
}

// prunedToolResults shrinks large tool outputs that are far from the tail.
func (a *Agent) prunedToolResults(history []llm.Message) []llm.Message {
	cfg := a.config().Compression
	minChars := cfg.ProactivePruneMinChars
	if minChars <= 0 {
		return history
	}
	protect := maxInt(cfg.ProtectLastN, 4)
	if len(history) <= protect+2 {
		return history
	}

	out := make([]llm.Message, len(history))
	copy(out, history)
	for i := 0; i < len(out)-protect; i++ {
		m := out[i]
		if m.Role != llm.RoleTool {
			continue
		}
		// Budget and notice both count characters, so the message never claims
		// to have dropped text it kept.
		chars := utf8.RuneCountInString(m.Content)
		if chars <= minChars {
			continue
		}
		out[i].Content = truncate(m.Content, minChars/2) +
			fmt.Sprintf("\n\n[tool result pruned: %d characters removed to free context]", chars-minChars/2)
	}
	return out
}

func truncate(s string, n int) string {
	out := textutil.TruncateRunes(s, n)
	if out == s {
		return s
	}
	return out + "…"
}
