package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func init() {
	for _, t := range []Tool{
		readFileTool{}, writeFileTool{}, editFileTool{}, listFilesTool{},
		globTool{}, grepTool{},
		terminalTool{},
		webFetchTool{}, webSearchTool{},
		memoryTool{}, sessionSearchTool{}, todoTool{}, skillTool{},
		ragSearchTool{}, ragIndexTool{},
		delegateTool{},
		browserTool{},
	} {
		globalRegistry.Register(t)
	}
}

// ToolsetsFor reports which named toolsets include a tool.
func ToolsetsFor(name string) []string {
	var out []string
	for set, members := range Toolsets {
		if set == "all" {
			continue
		}
		for _, m := range members {
			if m == name {
				out = append(out, set)
				break
			}
		}
	}
	return out
}

// NeedsApproval reports whether a tool mutates state.
func NeedsApproval(t Tool) bool {
	if a, ok := t.(Approval); ok {
		return a.RequiresApproval()
	}
	return false
}

// ---- delegate_task ----------------------------------------------------------

type delegateTool struct{}

func (delegateTool) Name() string { return "delegate_task" }
func (delegateTool) Description() string {
	return "Delegate a self-contained subtask to an isolated sub-agent with its own context. " +
		"Use it for parallel workstreams or research that would otherwise flood the main conversation. " +
		"The sub-agent returns only its final answer."
}
func (delegateTool) Schema() map[string]any {
	return schema(map[string]any{
		"prompt":       prop("string", "Complete, self-contained instructions. The sub-agent cannot see this conversation."),
		"toolset":      propEnum("Which tools the sub-agent may use.", "minimal", "coding", "research", "default"),
		"max_turns":    propDefault("integer", "Turn budget for the sub-agent.", 20),
		"context_note": prop("string", "Optional background the sub-agent needs."),
	}, "prompt")
}

func (delegateTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Prompt      string `json:"prompt"`
		Toolset     string `json:"toolset"`
		MaxTurns    int    `json:"max_turns"`
		ContextNote string `json:"context_note"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Sub == nil {
		return Errorf("delegation is not available in this runtime")
	}
	if !in.Deps.Config.Delegation.Enabled {
		return Errorf("delegation is disabled (delegation.enabled = false)")
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return Errorf("prompt is required")
	}
	if args.MaxTurns <= 0 {
		args.MaxTurns = 20
	}
	if maxIter := in.Deps.Config.Delegation.MaxIterations; args.MaxTurns > maxIter && maxIter > 0 {
		args.MaxTurns = maxIter
	}

	start := time.Now()
	in.Emit(Progress{Tool: "delegate_task", Message: "sub-agent started"})

	out, err := in.Deps.Sub(ctx, SubAgentRequest{
		Prompt:      prompt,
		SystemExtra: args.ContextNote,
		Toolset:     firstNonBlank(args.Toolset, "default"),
		MaxTurns:    args.MaxTurns,
		ParentID:    in.SessionID,
		OnProgress: func(p Progress) {
			in.Emit(Progress{Tool: "delegate_task", Message: p.Message, Chunk: p.Chunk})
		},
	})
	if err != nil {
		return Errorf("sub-agent failed: %v", err)
	}
	return Result{
		Content: out,
		Meta:    map[string]any{"duration_seconds": fmt.Sprintf("%.1f", time.Since(start).Seconds())},
	}
}
