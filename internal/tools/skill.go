package tools

import (
	"context"
	"fmt"
	"strings"
)

// skillTool lets the agent read a stored procedure and record new ones it
// learned, which is what closes the learning loop.
type skillTool struct{}

func (skillTool) Name() string { return "skill" }
func (skillTool) Description() string {
	return "Work with your skill library. `list` shows what you know, `read` loads a procedure before " +
		"following it, and `save` records a reusable procedure you just worked out. " +
		"Save a skill after solving something non-obvious that is likely to come up again."
}
func (skillTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":      propEnum("What to do.", "list", "read", "save"),
		"name":        prop("string", "Skill name (required for read and save)."),
		"description": prop("string", "For save: one line describing when this skill applies."),
		"body":        prop("string", "For save: the procedure itself, in Markdown. Be concrete — exact commands, paths, and gotchas."),
		"tags":        map[string]any{"type": "array", "description": "Optional tags.", "items": map[string]any{"type": "string"}},
	}, "action")
}

func (skillTool) RequiresApproval() bool { return false }

func (skillTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Action      string   `json:"action"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Body        string   `json:"body"`
		Tags        []string `json:"tags"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Skills == nil {
		return Errorf("the skill library is not available")
	}
	lib := in.Deps.Skills

	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list":
		items := lib.List()
		if len(items) == 0 {
			return Text("No skills stored yet. Use action=save once you work out something reusable.")
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d skill(s):\n\n", len(items))
		for _, s := range items {
			status := ""
			if !s.Enabled {
				status = " [disabled]"
			}
			fmt.Fprintf(&b, "- %s%s: %s\n", s.Name, status, s.Description)
		}
		return Text(b.String())

	case "read", "get":
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return Errorf("name is required when reading a skill")
		}
		info, body, ok := lib.Read(name)
		if !ok {
			return Errorf("skill %q not found. Use action=list to see what is available.", name)
		}
		lib.MarkUsed(name)
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n%s\n\n", info.Name, info.Description)
		if len(info.Triggers) > 0 {
			fmt.Fprintf(&b, "Use when: %s\n\n", strings.Join(info.Triggers, "; "))
		}
		b.WriteString(body)
		return Text(b.String())

	case "save", "write", "create":
		name := strings.TrimSpace(args.Name)
		body := strings.TrimSpace(args.Body)
		if name == "" || body == "" {
			return Errorf("both name and body are required when saving a skill")
		}
		if len(body) < 40 {
			return Errorf("the skill body is too thin to be useful — write the actual steps, commands, and pitfalls")
		}
		if err := lib.Write(name, args.Description, body, args.Tags); err != nil {
			return Errorf("save failed: %v", err)
		}
		return Text(fmt.Sprintf("Saved skill %q. It will appear in your catalogue on the next turn.", name))

	default:
		return Errorf("unknown action %q (want list, read, or save)", args.Action)
	}
}
