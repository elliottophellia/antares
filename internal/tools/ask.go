package tools

import (
	"context"
	"fmt"
	"strings"
)

// askUserTool lets the agent stop and put a question to the person, rather than
// guessing when a task is genuinely ambiguous. It is the counterpart to acting
// autonomously: most of the time the agent should find out for itself, but when
// only the user can decide — a missing requirement, a destructive choice, which
// of two paths — it asks and waits for the answer.
type askUserTool struct{}

func (askUserTool) Name() string { return "ask_user" }

func (askUserTool) Description() string {
	return "Ask the user a question and stop until they answer. Use it only when the task cannot proceed correctly " +
		"without a decision or a fact only they have — a missing requirement, an ambiguous target, or a choice between " +
		"real alternatives. Do not use it for things you can find out yourself. After calling it, end your turn; the " +
		"user's next message is the answer."
}

func (askUserTool) Schema() map[string]any {
	return schema(map[string]any{
		"question": prop("string", "The question to put to the user. Be specific and self-contained."),
		"options": map[string]any{
			"type": "array", "description": "Optional distinct choices to offer.",
			"items": map[string]any{"type": "string"},
		},
	}, "question")
}

func (askUserTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	q := strings.TrimSpace(args.Question)
	if q == "" {
		return Errorf("question is required")
	}

	// Surface the question to whatever is watching (dashboard, CLI, a gateway),
	// so it can render it — with options as buttons where the platform allows.
	msg := q
	if len(args.Options) > 0 {
		msg += " [" + strings.Join(args.Options, " / ") + "]"
	}
	in.Emit(Progress{Tool: "ask_user", Message: msg})

	// The result tells the model to pose the question and yield; the user's next
	// message becomes the answer. This is how a clarification blocks in a
	// conversational interface without a special round-trip.
	var b strings.Builder
	b.WriteString("Ask the user this question verbatim and then end your turn — do not continue until they reply:\n\n")
	b.WriteString(q)
	if len(args.Options) > 0 {
		b.WriteString("\n\nOffer these options:")
		for i, o := range args.Options {
			fmt.Fprintf(&b, "\n%d. %s", i+1, o)
		}
	}
	return Result{
		Content: b.String(),
		Meta:    map[string]any{"question": q, "options": args.Options},
	}
}
