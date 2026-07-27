package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// taskTool polls and manages the background sub-agents that delegate_task
// started with background=true.
type taskTool struct{}

func (taskTool) Name() string { return "task" }

func (taskTool) Description() string {
	return "Check on background sub-agents started with delegate_task(background=true). " +
		"`list` shows all tasks and their state, `status` reports one task, `output` returns a finished task's answer " +
		"(or says it is still running), and `stop` cancels one. Launch several long workstreams, keep working, then collect them here."
}

func (taskTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propEnum("What to do.", "list", "status", "output", "stop"),
		"id":     prop("string", "Task id, as returned by delegate_task(background=true). Required for status, output, and stop."),
	}, "action")
}

func (taskTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Action string `json:"action"`
		ID     string `json:"id"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Tasks == nil {
		return Errorf("background tasks are not available in this runtime")
	}
	tasks := in.Deps.Tasks
	id := strings.TrimSpace(args.ID)

	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list":
		all := tasks.List(in.SessionID)
		if len(all) == 0 {
			return Text("No background tasks. Start one with delegate_task(background=true).")
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d background task(s):\n\n", len(all))
		for _, t := range all {
			fmt.Fprintf(&b, "- %s [%s]%s: %s\n", t.ID, t.Status, roleTag(t.Role), t.Task)
		}
		return Text(b.String())

	case "status":
		if id == "" {
			return Errorf("id is required")
		}
		t, ok := tasks.Status(id)
		if !ok {
			return Errorf("no task %q — use action=list to see the ids", id)
		}
		took := ""
		if !t.EndedAt.IsZero() {
			took = fmt.Sprintf(", took %s", t.EndedAt.Sub(t.StartedAt).Round(time.Second))
		}
		return Text(fmt.Sprintf("Task %s is %s%s%s.\nTask: %s", t.ID, t.Status, roleTag(t.Role), took, t.Task))

	case "output", "result", "collect":
		if id == "" {
			return Errorf("id is required")
		}
		t, ok := tasks.Status(id)
		if !ok {
			return Errorf("no task %q — use action=list to see the ids", id)
		}
		switch t.Status {
		case "running":
			return Text(fmt.Sprintf("Task %s is still running. Keep working and check again shortly.", id))
		case "stopped":
			return Text(fmt.Sprintf("Task %s was stopped before it finished.", id))
		case "error":
			return Errorf("task %s failed: %s", id, t.Error)
		default:
			out := t.Output
			if strings.TrimSpace(out) == "" {
				out = "(the task produced no output)"
			}
			return Text(fmt.Sprintf("Task %s finished:\n\n%s", id, out))
		}

	case "stop", "cancel":
		if id == "" {
			return Errorf("id is required")
		}
		if !tasks.Stop(id) {
			return Errorf("no task %q — use action=list to see the ids", id)
		}
		return Text(fmt.Sprintf("Stopped task %s.", id))

	default:
		return Errorf("unknown action %q (want list, status, output, or stop)", args.Action)
	}
}

func roleTag(role string) string {
	if role == "" {
		return ""
	}
	return " (" + role + ")"
}
