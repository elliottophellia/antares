// Package agent implements the conversation loop: it builds the prompt, calls
// the model, executes the tools it asks for, and streams every step out.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/checkpoint"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/plugin"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// EventType enumerates what the agent streams to callers.
type EventType string

const (
	EventSession      EventType = "session"
	EventTurn         EventType = "turn"
	EventText         EventType = "text"
	EventReasoning    EventType = "reasoning"
	EventToolCall     EventType = "tool_call"
	EventToolProgress EventType = "tool_progress"
	EventToolResult   EventType = "tool_result"
	EventUsage        EventType = "usage"
	EventNotice       EventType = "notice"
	EventApproval     EventType = "approval"
	EventError        EventType = "error"
	EventDone         EventType = "done"
)

// Event is one streamed update. Fields are populated per Type.
type Event struct {
	Type EventType `json:"type"`

	// session
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`

	// text / reasoning
	Delta string `json:"delta,omitempty"`

	// tool_call / tool_progress / tool_result
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Content   string `json:"content,omitempty"`
	Chunk     string `json:"chunk,omitempty"`
	Message   string `json:"message,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// turn
	Turn int `json:"turn,omitempty"`

	// usage
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	Cost         float64 `json:"cost,omitempty"`

	// error
	Err string `json:"error,omitempty"`
}

// Emit sends events to the caller. Returning an error aborts the run.
type Emit func(Event) error

// Request starts or continues a conversation.
type Request struct {
	SessionID string
	Message   string
	Images    []llm.Part
	Platform  string
	UserID    string
	ChannelID string
	// Toolset overrides the configured toolset for this run.
	Toolset string
	// Model overrides the configured model for this run.
	Model string
	// MaxTurns overrides the configured turn budget.
	MaxTurns int
	// SystemExtra is appended to the system prompt (used by sub-agents).
	SystemExtra string
	// Quiet suppresses persistence, used for one-shot internal runs.
	Quiet bool
	// Depth guards against unbounded delegation recursion.
	Depth int
}

// Result summarises a completed run.
type Result struct {
	SessionID string
	Reply     string
	Turns     int
	Usage     llm.Usage
}

// Agent owns the shared services a run needs.
type Agent struct {
	cfg     *config.Config
	db      store.Store
	reg     *tools.Registry
	shell   *tools.ShellManager
	rag     tools.RAGProvider
	skills  *skills.Manager
	checks  *checkpoint.Store
	plugins *plugin.Manager

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

// New builds an agent.
func New(cfg *config.Config, db store.Store, reg *tools.Registry, shell *tools.ShellManager, ragProvider tools.RAGProvider) *Agent {
	return &Agent{
		cfg: cfg, db: db, reg: reg, shell: shell, rag: ragProvider,
		checks: checkpoint.NewStore(config.Path("checkpoints")),
		active: map[string]context.CancelFunc{},
	}
}

// Config exposes the live configuration.
func (a *Agent) Config() *config.Config { return a.cfg }

// SetConfig swaps in a reloaded configuration.
func (a *Agent) SetConfig(cfg *config.Config) { a.cfg = cfg }

// SetRAG swaps the retrieval provider after a config change.
func (a *Agent) SetRAG(p tools.RAGProvider) { a.rag = p }

// SetSkills attaches the skill library.
func (a *Agent) SetSkills(m *skills.Manager) { a.skills = m }

// Skills returns the skill library (may be nil).
func (a *Agent) Skills() *skills.Manager { return a.skills }

// RAG returns the active retrieval provider (may be nil).
func (a *Agent) RAG() tools.RAGProvider { return a.rag }

// Shell exposes the terminal manager for lifecycle handling.
func (a *Agent) Shell() *tools.ShellManager { return a.shell }

// Registry exposes the tool registry.
func (a *Agent) Registry() *tools.Registry { return a.reg }

// Interrupt cancels a running turn for a session.
func (a *Agent) Interrupt(sessionID string) bool {
	a.mu.Lock()
	cancel, ok := a.active[sessionID]
	a.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// ActiveCount reports how many turns are running.
func (a *Agent) ActiveCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.active)
}

func newID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// Run executes one user turn to completion, streaming progress through emit.
func (a *Agent) Run(ctx context.Context, req Request, emit Emit) (*Result, error) {
	if emit == nil {
		emit = func(Event) error { return nil }
	}
	cfg := a.cfg

	sess, err := a.resolveSession(ctx, &req)
	if err != nil {
		return nil, err
	}
	if !req.Quiet {
		if err := emit(Event{Type: EventSession, ID: sess.ID, Title: sess.Title}); err != nil {
			return nil, err
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.mu.Lock()
	a.active[sess.ID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.active, sess.ID)
		a.mu.Unlock()
	}()

	client, modelName, providerName, err := a.newClient(req.Model)
	if err != nil {
		_ = emit(Event{Type: EventError, Err: err.Error()})
		_ = emit(Event{Type: EventDone})
		return nil, err
	}

	history, err := a.loadHistory(ctx, sess.ID, req)
	if err != nil {
		return nil, err
	}

	// Persist the user turn before calling the model so a crash mid-run does not
	// lose it.
	userMsg := llm.Message{Role: llm.RoleUser, Content: req.Message, Parts: req.Images}
	if !req.Quiet {
		attachments := ""
		if len(req.Images) > 0 {
			if b, err := json.Marshal(req.Images); err == nil {
				attachments = string(b)
			}
		}
		if err := a.db.AppendMessage(ctx, &store.Message{
			ID: newID("msg"), SessionID: sess.ID, Role: store.RoleUser,
			Content: req.Message, Attachments: attachments,
		}); err != nil {
			slog.Warn("persist user message failed", "error", err)
		}
	}
	history = append(history, userMsg)

	activeTools := a.resolveTools(req)
	toolSpecs := make([]llm.Tool, 0, len(activeTools))
	byName := make(map[string]tools.Tool, len(activeTools))
	for _, t := range activeTools {
		toolSpecs = append(toolSpecs, llm.Tool{Name: t.Name(), Description: t.Description(), Parameters: t.Schema()})
		byName[t.Name()] = t
	}

	systemPrompt := a.buildSystemPrompt(ctx, req, sess, activeTools)

	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = cfg.Agent.MaxTurns
	}
	if maxTurns <= 0 {
		maxTurns = 50
	}

	var (
		total     llm.Usage
		lastReply string
		turn      int
		toolCalls int
		verified  int
		judged    int
	)
	repeats := newRepeatTracker(cfg.Agent.RepeatLimit)
	goal, hasGoal := a.GetGoal(ctx, sess.ID)
	if hasGoal && (goal.Paused || goal.Done) {
		hasGoal = false
	}

	for turn = 1; turn <= maxTurns; turn++ {
		if err := runCtx.Err(); err != nil {
			break
		}
		if turn > 1 {
			if err := emit(Event{Type: EventTurn, Turn: turn}); err != nil {
				return nil, err
			}
		}

		history = a.maybeCompact(runCtx, history, systemPrompt, emit)

		llmReq := llm.Request{
			Model:             modelName,
			System:            systemPrompt,
			Messages:          history,
			Tools:             toolSpecs,
			Temperature:       cfg.Model.Temperature,
			TopP:              cfg.Model.TopP,
			MaxTokens:         cfg.Model.MaxTokens,
			StopSequences:     cfg.Agent.StopSequences,
			ReasoningEffort:   firstNonEmpty(cfg.Agent.ReasoningEffort, cfg.Model.ReasoningEffort),
			ParallelToolCalls: cfg.Model.ParallelToolCall,
			PromptCache:       cfg.PromptCaching.Enabled,
		}

		resp, err := a.callModel(runCtx, client, llmReq, cfg.Streaming.Enabled, emit)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				_ = emit(Event{Type: EventNotice, Message: "interrupted"})
				break
			}
			// Run owns the terminal events so callers never double-report.
			_ = emit(Event{Type: EventError, Err: err.Error()})
			_ = emit(Event{Type: EventDone})
			return nil, err
		}

		total.InputTokens += resp.Usage.InputTokens
		total.OutputTokens += resp.Usage.OutputTokens
		total.CacheReadTokens += resp.Usage.CacheReadTokens
		if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
			_ = emit(Event{
				Type: EventUsage, InputTokens: total.InputTokens, OutputTokens: total.OutputTokens,
			})
			if !req.Quiet {
				a.recordUsage(ctx, sess.ID, providerName, modelName, resp.Usage)
			}
		}

		assistant := llm.Message{
			Role: llm.RoleAssistant, Content: resp.Content,
			Reasoning: resp.Reasoning, ToolCalls: resp.ToolCalls,
		}
		history = append(history, assistant)
		if !req.Quiet {
			a.persistAssistant(ctx, sess.ID, modelName, resp)
		}
		if resp.Content != "" {
			lastReply = resp.Content
		}

		if len(resp.ToolCalls) == 0 {
			// The model thinks it is finished. Before believing it, check the
			// work, then check whether a standing goal is actually met.
			if follow := a.followUp(runCtx, req, sess, history, lastReply, goal, hasGoal, &verified, &judged, emit); follow != "" {
				history = append(history, llm.Message{Role: llm.RoleUser, Content: follow})
				continue
			}
			break
		}

		toolCalls += len(resp.ToolCalls)
		if a.guardrailTripped(toolCalls, emit) {
			history = append(history, llm.Message{
				Role:    llm.RoleUser,
				Content: "Tool-call budget reached. Summarise what you have found and stop calling tools.",
			})
			continue
		}

		if stuck := repeats.record(resp.ToolCalls); len(stuck) > 0 {
			if repeats.exceeded() {
				_ = emit(Event{Type: EventNotice, Message: "stopped: the same tool call kept repeating"})
				lastReply = "I was repeating the same step without making progress, so I stopped. " +
					"Tell me what to try differently."
				break
			}
			_ = emit(Event{Type: EventNotice, Message: "repeating " + strings.Join(stuck, ", ")})
			history = append(history, llm.Message{
				Role: llm.RoleUser,
				Content: "You have called " + strings.Join(stuck, " and ") +
					" with the same arguments several times and it is not getting you anywhere. " +
					"Do not call it again. Either try a different approach, or say what is blocking you.",
			})
		}

		results := a.executeTools(runCtx, resp.ToolCalls, byName, req, sess, emit)
		for _, r := range results {
			history = append(history, r.message)
			if !req.Quiet {
				if err := a.db.AppendMessage(ctx, &store.Message{
					ID: newID("msg"), SessionID: sess.ID, Role: store.RoleTool,
					Content: r.message.Content, ToolCallID: r.message.ToolCallID, ToolName: r.message.Name,
					Meta: store.Meta{"is_error": r.isError},
				}); err != nil {
					slog.Warn("persist tool result failed", "error", err)
				}
			}
		}

		// Notes typed while this run was already going land here, which is the
		// first point the model can act on them without discarding work.
		for _, note := range drainSteering(sess.ID) {
			_ = emit(Event{Type: EventNotice, Message: "steering: " + note})
			history = append(history, llm.Message{
				Role:    llm.RoleUser,
				Content: "A new instruction arrived while you were working: " + note,
			})
		}
	}

	if turn > maxTurns {
		_ = emit(Event{Type: EventNotice, Message: fmt.Sprintf("turn limit reached (%d)", maxTurns)})
	}

	if !req.Quiet {
		a.maybeTitle(ctx, sess, req.Message, lastReply)
		if err := emit(Event{Type: EventSession, ID: sess.ID, Title: sess.Title}); err != nil {
			return nil, err
		}
	}
	_ = emit(Event{Type: EventDone})

	return &Result{SessionID: sess.ID, Reply: lastReply, Turns: turn, Usage: total}, nil
}

// callModel runs one completion, streaming when enabled.
func (a *Agent) callModel(ctx context.Context, client llm.Client, req llm.Request, stream bool, emit Emit) (*llm.Response, error) {
	if !stream {
		resp, err := client.Chat(ctx, req)
		if err != nil {
			return nil, err
		}
		if resp.Reasoning != "" {
			_ = emit(Event{Type: EventReasoning, Delta: resp.Reasoning})
		}
		if resp.Content != "" {
			_ = emit(Event{Type: EventText, Delta: resp.Content})
		}
		return resp, nil
	}

	return client.Stream(ctx, req, func(ev llm.Event) error {
		switch ev.Type {
		case llm.EventText:
			return emit(Event{Type: EventText, Delta: ev.Delta})
		case llm.EventReasoning:
			if a.cfg.Display.ShowReasoning {
				return emit(Event{Type: EventReasoning, Delta: ev.Delta})
			}
		}
		return nil
	})
}

type toolOutcome struct {
	message llm.Message
	isError bool
}

// executeTools runs the requested calls, in parallel when the config allows.
func (a *Agent) executeTools(
	ctx context.Context,
	calls []llm.ToolCall,
	byName map[string]tools.Tool,
	req Request,
	sess *store.Session,
	emit Emit,
) []toolOutcome {
	outcomes := make([]toolOutcome, len(calls))

	// Emit all call announcements up front so the UI can render them in order.
	for _, call := range calls {
		_ = emit(Event{Type: EventToolCall, ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}

	// Serialise emits: tools run concurrently but events must not interleave
	// mid-write.
	var emitMu sync.Mutex
	safeEmit := func(e Event) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		return emit(e)
	}

	run := func(i int, call llm.ToolCall) {
		tool, ok := byName[call.Name]
		if !ok {
			outcomes[i] = toolOutcome{
				message: llm.Message{
					Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
					Content: fmt.Sprintf("Tool %q is not available. Active tools: %s", call.Name, strings.Join(namesOf(byName), ", ")),
				},
				isError: true,
			}
			_ = safeEmit(Event{Type: EventToolResult, ID: call.ID, Name: call.Name, Content: outcomes[i].message.Content, IsError: true})
			return
		}

		// A tool that changes something may need a person to say yes first.
		if refusal := a.checkApproval(ctx, call, tool, sess.ID, safeEmit); refusal != nil {
			outcomes[i] = toolOutcome{
				message: llm.Message{
					Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
					Content: refusal.Content,
				},
				isError: true,
			}
			_ = safeEmit(Event{
				Type: EventToolResult, ID: call.ID, Name: call.Name,
				Content: refusal.Content, IsError: true,
			})
			return
		}

		// Plugins see the call before it runs, and may refuse it or change
		// its arguments.
		if a.plugins != nil {
			hook := a.plugins.Dispatch(ctx, plugin.Payload{
				Event: plugin.PreToolCall, SessionID: sess.ID, Platform: req.Platform,
				Tool: call.Name, Arguments: call.Arguments,
			})
			if hook.Notice != "" {
				_ = safeEmit(Event{Type: EventNotice, Message: hook.Notice})
			}
			if hook.Deny {
				content := "refused by policy: " + hook.Reason
				outcomes[i] = toolOutcome{
					message: llm.Message{
						Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name,
						Content: content,
					},
					isError: true,
				}
				_ = safeEmit(Event{
					Type: EventToolResult, ID: call.ID, Name: call.Name,
					Content: content, IsError: true,
				})
				return
			}
			if hook.Arguments != "" {
				call.Arguments = hook.Arguments
			}
		}

		workspace := sess.Workspace
		if workspace == "" {
			workspace = a.cfg.Agent.Workspace
		}
		in := tools.Input{
			Args:      json.RawMessage(call.Arguments),
			CallID:    call.ID,
			SessionID: sess.ID,
			UserID:    req.UserID,
			Platform:  req.Platform,
			Workspace: workspace,
			Emit: func(p tools.Progress) {
				_ = safeEmit(Event{
					Type: EventToolProgress, ID: call.ID, Name: call.Name,
					Chunk: p.Chunk, Message: p.Message,
				})
			},
			Deps: &tools.Deps{
				Config: a.cfg, Store: a.db, RAG: a.rag, Shell: a.shell,
				Sub: a.subAgentFor(req), Skills: a.skillLibrary(),
				Checkpoint: a.saveCheckpoint,
			},
		}

		timeout := a.toolTimeout(call.Name)
		toolCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		start := time.Now()
		res := tool.Execute(toolCtx, in)
		content := trimForModel(res.Content, a.cfg.Tools.MaxOutputChars)
		if content == "" {
			content = "(tool produced no output)"
		}
		slog.Debug("tool executed", "tool", call.Name, "ms", time.Since(start).Milliseconds(), "error", res.IsError)

		// Plugins see the result and may replace what the model is shown —
		// redacting a secret out of a log, for instance.
		if a.plugins != nil {
			hook := a.plugins.Dispatch(ctx, plugin.Payload{
				Event: plugin.PostToolCall, SessionID: sess.ID, Platform: req.Platform,
				Tool: call.Name, Arguments: call.Arguments,
				Result: content, IsError: res.IsError,
			})
			if hook.Notice != "" {
				_ = safeEmit(Event{Type: EventNotice, Message: hook.Notice})
			}
			if hook.Result != "" {
				content = hook.Result
			}
		}

		outcomes[i] = toolOutcome{
			message: llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: content},
			isError: res.IsError,
		}
		_ = safeEmit(Event{Type: EventToolResult, ID: call.ID, Name: call.Name, Content: content, IsError: res.IsError})
	}

	parallel := a.cfg.Model.ParallelToolCall && len(calls) > 1
	if parallel {
		var wg sync.WaitGroup
		sem := make(chan struct{}, maxParallelTools)
		for i, call := range calls {
			wg.Add(1)
			go func(i int, call llm.ToolCall) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				run(i, call)
			}(i, call)
		}
		wg.Wait()
		return outcomes
	}
	for i, call := range calls {
		run(i, call)
	}
	return outcomes
}

const maxParallelTools = 4

func (a *Agent) toolTimeout(name string) time.Duration {
	if secs, ok := a.cfg.Tools.Timeouts[name]; ok && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	switch name {
	case "terminal":
		return time.Duration(maxInt(a.cfg.Terminal.Timeout, 60)) * time.Second
	case "delegate_task":
		return 30 * time.Minute
	default:
		return 5 * time.Minute
	}
}

// subAgentFor returns a delegation hook bound to the current run's depth.
func (a *Agent) subAgentFor(parent Request) tools.SubAgent {
	return func(ctx context.Context, sub tools.SubAgentRequest) (string, error) {
		depth := parent.Depth + 1
		if maxDepth := a.cfg.Delegation.MaxDepth; maxDepth > 0 && depth > maxDepth {
			return "", fmt.Errorf("maximum delegation depth (%d) reached", maxDepth)
		}
		res, err := a.Run(ctx, Request{
			Message:     sub.Prompt,
			SystemExtra: sub.SystemExtra,
			Toolset:     sub.Toolset,
			MaxTurns:    sub.MaxTurns,
			Platform:    "subagent",
			UserID:      parent.UserID,
			Quiet:       true,
			Depth:       depth,
		}, func(e Event) error {
			if sub.OnProgress != nil {
				switch e.Type {
				case EventToolCall:
					sub.OnProgress(tools.Progress{Message: "sub-agent: " + e.Name})
				case EventNotice:
					sub.OnProgress(tools.Progress{Message: "sub-agent: " + e.Message})
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(res.Reply) == "" {
			return "(sub-agent finished without a final answer)", nil
		}
		return res.Reply, nil
	}
}

func (a *Agent) guardrailTripped(toolCalls int, emit Emit) bool {
	g := a.cfg.Guardrails
	if g.WarningsEnabled && g.WarnAfter > 0 && toolCalls == g.WarnAfter {
		_ = emit(Event{Type: EventNotice, Message: fmt.Sprintf("%d tool calls in this turn", toolCalls)})
	}
	if g.HardStopEnabled && g.HardStopAfter > 0 && toolCalls >= g.HardStopAfter {
		_ = emit(Event{Type: EventNotice, Message: "hard tool-call limit reached"})
		return true
	}
	return false
}

func namesOf(m map[string]tools.Tool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func trimForModel(s string, limit int) string {
	if limit <= 0 {
		limit = 60000
	}
	if len(s) <= limit {
		return s
	}
	head := limit * 2 / 3
	tail := limit - head
	return s[:head] + fmt.Sprintf("\n\n… %d characters truncated …\n\n", len(s)-limit) + s[len(s)-tail:]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
