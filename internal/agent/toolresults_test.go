package agent

import (
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func toolIDs(msgs []llm.Message) []string {
	var ids []string
	for _, m := range msgs {
		if m.Role == llm.RoleTool {
			ids = append(ids, m.ToolCallID)
		}
	}
	return ids
}

// countStubs reports how many tool messages carry the interrupted-stub marker.
func countStubs(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.Content == "[no result recorded — the previous run was interrupted before this tool finished]" {
			n++
		}
	}
	return n
}

func TestEnsureToolResults_StubsDanglingCall(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleUser, Content: "do it"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "ok"},
		// "b" never got a result — interrupted.
	}
	out := ensureToolResults(in)
	if got := countStubs(out); got != 1 {
		t.Fatalf("want 1 stub, got %d (%v)", got, toolIDs(out))
	}
	// Every call id must now have a following tool message.
	ids := map[string]bool{}
	for _, m := range out {
		if m.Role == llm.RoleTool {
			ids[m.ToolCallID] = true
		}
	}
	if !ids["a"] || !ids["b"] {
		t.Fatalf("missing tool result: %v", ids)
	}
}

func TestEnsureToolResults_LeavesCompleteTurnsUntouched(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "ok"},
		{Role: llm.RoleTool, ToolCallID: "b", Content: "ok"},
		{Role: llm.RoleAssistant, Content: "done"},
	}
	out := ensureToolResults(in)
	if len(out) != len(in) {
		t.Fatalf("complete history changed: %d -> %d", len(in), len(out))
	}
	if countStubs(out) != 0 {
		t.Fatalf("unexpected stub inserted")
	}
}

func TestEnsureToolResults_NoToolCalls(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
	}
	out := ensureToolResults(in)
	if len(out) != 2 || countStubs(out) != 0 {
		t.Fatalf("plain conversation altered: %+v", out)
	}
}
