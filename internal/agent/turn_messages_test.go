package agent

import (
	"reflect"
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func toolOutcomes(ids ...string) []toolOutcome {
	out := make([]toolOutcome, 0, len(ids))
	for _, id := range ids {
		out = append(out, toolOutcome{
			message: llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: "read_file", Content: "result " + id},
		})
	}
	return out
}

// The repetition nudge used to be appended before the tools ran, which put a
// user message between the assistant's tool_calls and their results. That is
// the malformation ensureToolResults repairs at send time, and because the
// repair is silent the whole suite stayed green while the transcript being
// built was invalid. This is the assertion that notices.
func TestAppendTurnMessagesKeepsEveryToolResultBeforeTheNudge(t *testing.T) {
	const nudge = "You have called read_file with the same arguments several times."

	out := appendTurnMessages(
		[]llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1"}, {ID: "c2"}}}},
		toolOutcomes("c1", "c2"),
		nudge,
		[]string{"a steering note"},
	)

	nudgeAt := -1
	for i, m := range out {
		if m.Role == llm.RoleUser && m.Content == nudge {
			nudgeAt = i
			break
		}
	}
	if nudgeAt < 0 {
		t.Fatalf("the nudge was never appended: %q", roleAndContent(out))
	}

	for i, m := range out {
		if m.Role == llm.RoleTool && i > nudgeAt {
			t.Fatalf("tool result %q lands at %d, after the nudge at %d — a user message between an "+
				"assistant's tool_calls and their results is not a valid transcript: %q",
				m.ToolCallID, i, nudgeAt, roleAndContent(out))
		}
	}
}

func TestAppendTurnMessagesOrdersResultsThenNudgeThenNotes(t *testing.T) {
	out := appendTurnMessages(nil, toolOutcomes("c1", "c2"), "stop repeating", []string{"first note", "second note"})

	want := []string{
		"tool:result c1",
		"tool:result c2",
		"user:stop repeating",
		"user:A new instruction arrived while you were working: first note",
		"user:A new instruction arrived while you were working: second note",
	}
	if got := roleAndContent(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn tail assembled out of order:\n got %q\nwant %q", got, want)
	}
}

func TestAppendTurnMessagesOmitsAnAbsentNudge(t *testing.T) {
	out := appendTurnMessages(nil, toolOutcomes("c1"), "", nil)

	want := []string{"tool:result c1"}
	if got := roleAndContent(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("an empty nudge became a message:\n got %q\nwant %q", got, want)
	}
}
