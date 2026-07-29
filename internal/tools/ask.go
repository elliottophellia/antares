package tools

import (
	"context"
	"fmt"
	"strings"
)

// askUserTool lets the agent stop and put one or more questions to the person,
// rather than guessing when a task is genuinely ambiguous. It is the
// counterpart to acting autonomously: most of the time the agent should find
// out for itself, but when only the user can decide — a missing requirement, a
// destructive choice, which of two paths — it asks and waits for the answer.
type askUserTool struct{}

func (askUserTool) Name() string { return "ask_user" }

func (askUserTool) Description() string {
	return "Ask the user one or more questions and stop until they answer. Use it only when the task cannot proceed " +
		"correctly without a decision or a fact only they have — a missing requirement, an ambiguous target, or a " +
		"choice between real alternatives. Do not use it for things you can find out yourself. Prefer `questions` " +
		"(an array) to gather everything you need in one pass — the UI shows them one at a time with next/previous " +
		"and submits all answers together — rather than calling this tool repeatedly. After calling it, end your " +
		"turn; the user's next message carries the answers."
}

// askQuestion is one question in the array form.
func askQuestionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": prop("string", "The question to put to the user. Be specific and self-contained."),
			"header":   prop("string", "Optional short label for this question (a few words), shown as a heading."),
			"options": map[string]any{
				"type": "array", "description": "Optional distinct choices to offer for this question.",
				"items": map[string]any{"type": "string"},
			},
			"multiSelect": prop("boolean", "Allow more than one option to be chosen for this question."),
		},
		"required": []string{"question"},
	}
}

func (askUserTool) Schema() map[string]any {
	return schema(map[string]any{
		// Single-question form (kept for simple asks and backward compatibility).
		"question": prop("string", "A single question to ask. For more than one, use `questions` instead."),
		"options": map[string]any{
			"type": "array", "description": "Optional distinct choices for the single `question`.",
			"items": map[string]any{"type": "string"},
		},
		// Multi-question form: shown one at a time with next/previous, answered
		// together. Prefer this when you need several decisions at once.
		"questions": map[string]any{
			"type":        "array",
			"description": "Several questions to ask in one pass. Preferred over calling this tool repeatedly.",
			"items":       askQuestionSchema(),
		},
	})
}

type askQuestion struct {
	Question    string   `json:"question"`
	Header      string   `json:"header"`
	Options     []string `json:"options"`
	MultiSelect bool     `json:"multiSelect"`
}

func (askUserTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Question  string        `json:"question"`
		Options   []string      `json:"options"`
		Questions []askQuestion `json:"questions"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}

	// Normalise to a single list. The single-question fields fold into it so the
	// rest of the flow (and the frontend) only ever deals with `questions`.
	qs := args.Questions
	if len(qs) == 0 {
		q := strings.TrimSpace(args.Question)
		if q == "" {
			return Errorf("provide `question` or a non-empty `questions` array")
		}
		qs = []askQuestion{{Question: q, Options: args.Options}}
	}
	// Drop blank entries so a stray "" does not create an unanswerable step.
	cleaned := qs[:0]
	for _, q := range qs {
		if strings.TrimSpace(q.Question) != "" {
			cleaned = append(cleaned, q)
		}
	}
	if len(cleaned) == 0 {
		return Errorf("every question was empty")
	}
	qs = cleaned

	// Preferred path: a person is watching, so pause the turn and wait for the
	// answer, which comes back as this tool's result — the SAME turn then
	// continues. No new chat message, no ended turn.
	if in.AskUser != nil {
		conv := make([]AskQuestion, len(qs))
		for i, q := range qs {
			conv[i] = AskQuestion{Question: q.Question, Header: q.Header, Options: q.Options, MultiSelect: q.MultiSelect}
		}
		answer, err := in.AskUser(ctx, conv)
		if err != nil {
			// The run was cancelled (stopped, or the socket closed) before an
			// answer arrived. Report it plainly rather than inventing one.
			return Errorf("the question was not answered (the run was interrupted): %v", err)
		}
		return Result{Content: "The user answered:\n\n" + answer}
	}

	// Fallback (no interactive channel — some gateways, cron): surface the
	// questions and yield, letting the next inbound message be the answer.
	var parts []string
	for _, q := range qs {
		p := strings.TrimSpace(q.Question)
		if len(q.Options) > 0 {
			p += " [" + strings.Join(q.Options, " / ") + "]"
		}
		parts = append(parts, p)
	}
	in.Emit(Progress{Tool: "ask_user", Message: strings.Join(parts, " · ")})

	var b strings.Builder
	if len(qs) == 1 {
		b.WriteString("Ask the user this question verbatim and then end your turn — do not continue until they reply:\n\n")
		b.WriteString(qs[0].Question)
		if len(qs[0].Options) > 0 {
			b.WriteString("\n\nOffer these options:")
			for i, o := range qs[0].Options {
				fmt.Fprintf(&b, "\n%d. %s", i+1, o)
			}
		}
	} else {
		b.WriteString("Ask the user these questions and then end your turn — do not continue until they reply. ")
		b.WriteString("The interface shows them one at a time and returns all answers together:\n")
		for i, q := range qs {
			fmt.Fprintf(&b, "\n%d. %s", i+1, strings.TrimSpace(q.Question))
			if len(q.Options) > 0 {
				fmt.Fprintf(&b, " (options: %s)", strings.Join(q.Options, ", "))
			}
		}
	}

	// Meta mirrors the normalised list so non-web surfaces can render it too.
	metaQs := make([]map[string]any, 0, len(qs))
	for _, q := range qs {
		metaQs = append(metaQs, map[string]any{
			"question": q.Question, "header": q.Header,
			"options": q.Options, "multiSelect": q.MultiSelect,
		})
	}
	return Result{
		Content: b.String(),
		Meta:    map[string]any{"questions": metaQs},
	}
}
