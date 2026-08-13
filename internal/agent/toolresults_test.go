package agent

import (
	"reflect"
	"strings"
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

// countStubs reports how many tool messages carry the missing-result marker.
func countStubs(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.Content == "[no result was recorded for this tool call]" {
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

func TestEnsureToolResultsMatchesByCallID(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "read_file"},
			{ID: "c2", Name: "grep"},
		}},
		{Role: llm.RoleUser, Content: "a nudge that slipped in"},
		{Role: llm.RoleTool, ToolCallID: "c2", Name: "grep", Content: "GREP OUT"},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "read_file", Content: "FILE OUT"},
	}

	out := ensureToolResults(history)

	// The assistant turn is followed immediately by its results, in call order.
	var ai int = -1
	for i, m := range out {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) == 2 {
			ai = i
		}
	}
	if ai < 0 {
		t.Fatal("assistant turn missing")
	}
	if out[ai+1].ToolCallID != "c1" || out[ai+1].Content != "FILE OUT" {
		t.Fatalf("first result = %+v", out[ai+1])
	}
	if out[ai+2].ToolCallID != "c2" || out[ai+2].Content != "GREP OUT" {
		t.Fatalf("second result = %+v", out[ai+2])
	}
	// The interleaved user message survives, after the results.
	found := false
	for _, m := range out[ai+3:] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "nudge") {
			found = true
		}
	}
	if !found {
		t.Fatal("interleaved user message was dropped")
	}
}

func TestEnsureToolResultsStubsOnlyMissingCallsAndTellsTheTruth(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "read_file"},
			{ID: "c2", Name: "grep"},
		}},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "read_file", Content: "FILE OUT"},
	}

	out := ensureToolResults(history)

	var stub string
	for _, m := range out {
		if m.ToolCallID == "c2" {
			stub = m.Content
		}
		if m.ToolCallID == "c1" && m.Content != "FILE OUT" {
			t.Fatalf("real result was replaced: %q", m.Content)
		}
	}
	if stub == "" {
		t.Fatal("missing call c2 was not stubbed")
	}
	if strings.Contains(stub, "interrupted") {
		t.Fatalf("stub asserts an interruption that did not happen: %q", stub)
	}
}

func TestEnsureToolResultsDropsResultsWithNoMatchingCall(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleTool, ToolCallID: "orphan", Name: "read_file", Content: "x"},
	}
	for _, m := range ensureToolResults(history) {
		if m.Role == llm.RoleTool {
			t.Fatalf("orphan tool result was kept: %+v", m)
		}
	}
}

// Gemini synthesises a call id from the call's position and name when it omits
// one, so two turns that call the same tool first recur under the same id.
// Each turn must still be answered with its own result.
func TestEnsureToolResults_RepeatedIDAcrossTurnsKeepsBothResults(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_0_read_file", Name: "read_file"}}},
		{Role: llm.RoleTool, ToolCallID: "call_0_read_file", Name: "read_file", Content: "FIRST"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_0_read_file", Name: "read_file"}}},
		{Role: llm.RoleTool, ToolCallID: "call_0_read_file", Name: "read_file", Content: "SECOND"},
	}
	out := ensureToolResults(in)
	var got []string
	for _, m := range out {
		if m.Role == llm.RoleTool {
			got = append(got, m.Content)
		}
	}
	if len(got) != 2 || got[0] != "FIRST" || got[1] != "SECOND" {
		t.Fatalf("tool results = %q, want [FIRST SECOND]", got)
	}
	if countStubs(out) != 0 {
		t.Fatalf("a real result was stubbed: %+v", out)
	}
}

func TestEnsureToolResults_SecondResultForOneCallIsDropped(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "grep"}}},
		{Role: llm.RoleTool, ToolCallID: "a", Name: "grep", Content: "first"},
		{Role: llm.RoleTool, ToolCallID: "a", Name: "grep", Content: "second"},
	}
	out := ensureToolResults(in)
	if ids := toolIDs(out); len(ids) != 1 {
		t.Fatalf("want one tool message, got %d (%v)", len(ids), ids)
	}
	for _, m := range out {
		if m.Role == llm.RoleTool && m.Content != "first" {
			t.Fatalf("later duplicate won: %q", m.Content)
		}
	}
}

func TestEnsureToolResults_InterleavedMessagesKeepTheirOrder(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "grep"}}},
		{Role: llm.RoleUser, Content: "first nudge"},
		{Role: llm.RoleUser, Content: "second nudge"},
		{Role: llm.RoleTool, ToolCallID: "a", Name: "grep", Content: "ok"},
	}
	out := ensureToolResults(in)
	var order []string
	for _, m := range out {
		order = append(order, m.Role+":"+m.Content)
	}
	want := []string{"assistant:", "tool:ok", "user:first nudge", "user:second nudge"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %q, want %q", order, want)
	}
}

// The repair applies to what is sent, so the history it reads — which is what
// gets persisted — must come back untouched.
func TestEnsureToolResults_DoesNotMutateItsInput(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "grep"}, {ID: "b", Name: "read_file"}}},
		{Role: llm.RoleUser, Content: "nudge"},
		{Role: llm.RoleTool, ToolCallID: "a", Name: "grep", Content: "ok"},
	}
	before := append([]llm.Message(nil), in...)
	ensureToolResults(in)
	if !reflect.DeepEqual(in, before) {
		t.Fatalf("input history was rewritten:\n got %+v\nwant %+v", in, before)
	}
}
