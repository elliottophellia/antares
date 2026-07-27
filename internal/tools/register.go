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
		listRolesTool{},
		scopeCheckTool{},
		reportFindingTool{},
		addIntelTool{},
		methodologyStatusTool{},
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
		"role":         prop("string", "Optional specialist to run this as: coder, reviewer, researcher, writer, planner, and others. Sets the sub-agent's instructions, tools, and model. Use list_roles-style knowledge or leave empty for a general sub-agent."),
		"toolset":      propEnum("Which tools the sub-agent may use. Overrides the role's toolset.", "minimal", "coding", "research", "default"),
		"max_turns":    propDefault("integer", "Turn budget for the sub-agent.", 20),
		"context_note": prop("string", "Optional background the sub-agent needs."),
		"isolate":      propDefault("boolean", "Run the sub-agent in its own git worktree, so parallel sub-agents editing the same repository do not conflict. Only works in a git repository.", false),
	}, "prompt")
}

func (delegateTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Prompt      string `json:"prompt"`
		Role        string `json:"role"`
		Toolset     string `json:"toolset"`
		MaxTurns    int    `json:"max_turns"`
		ContextNote string `json:"context_note"`
		Isolate     bool   `json:"isolate"`
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
		Role:        args.Role,
		Toolset:     args.Toolset,
		Isolate:     args.Isolate,
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

// ---- list_roles -------------------------------------------------------------

type listRolesTool struct{}

func (listRolesTool) Name() string { return "list_roles" }
func (listRolesTool) Description() string {
	return "List the specialist roles you can delegate to. Each role has its own instructions, tools, and often its own model. " +
		"Call this before delegating a self-contained piece of work so you can hand it to the right specialist with delegate_task's role parameter."
}
func (listRolesTool) Schema() map[string]any { return schema(map[string]any{}) }

func (listRolesTool) Execute(_ context.Context, in Input) Result {
	if in.Deps == nil || in.Deps.Roles == nil {
		return Text("No roles are available in this runtime.")
	}
	list := in.Deps.Roles()
	if len(list) == 0 {
		return Text("No roles are defined.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d role(s) available for delegation:\n\n", len(list))
	for _, r := range list {
		mark := ""
		if r.Danger {
			mark = " (authorized security testing only)"
		}
		fmt.Fprintf(&b, "- %s%s — %s\n", r.Name, mark, r.Summary)
	}
	b.WriteString("\nDelegate to one with delegate_task, passing its name as the role parameter.")
	return Text(b.String())
}
