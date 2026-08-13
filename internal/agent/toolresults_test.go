package agent

import (
	"encoding/json"
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

// noResultStub is the content ensureToolResults gives a call the transcript
// holds no result for. Spelled out here rather than shared with the production
// constant so a reworded stub has to be reviewed, not just propagated.
const noResultStub = "[no result was recorded for this tool call]"

// countStubs reports how many tool messages carry the missing-result marker.
func countStubs(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.Content == noResultStub {
			n++
		}
	}
	return n
}

// roleAndContent renders the emitted sequence for tests that pin whole shapes.
func roleAndContent(msgs []llm.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Role+":"+m.Content)
	}
	return out
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
	// Pinned exactly: the stub may state that no result was recorded and
	// nothing else. Any other wording — an interruption, a failure, a refusal —
	// tells the model something the transcript does not support.
	if stub != noResultStub {
		t.Fatalf("stub = %q, want %q", stub, noResultStub)
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
	order := roleAndContent(ensureToolResults(in))
	want := []string{"assistant:", "tool:ok", "user:first nudge", "user:second nudge"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %q, want %q", order, want)
	}
}

// Compaction splices the first protectFirstN messages in verbatim
// (compact.go:73,104) and rebalanceToolBoundary only repairs the middle/tail
// boundary, so the head can end on a tool-call turn whose result was
// summarised away. That dangling call must not reach forward and take the
// result belonging to the live call of the same id.
func TestEnsureToolResults_DanglingHeadCallDoesNotStealALaterResult(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_0_read_file", Name: "read_file"}}},
		{Role: llm.RoleUser, Content: "[Compacted summary of the earlier conversation]"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_0_read_file", Name: "read_file"}}},
		{Role: llm.RoleTool, ToolCallID: "call_0_read_file", Name: "read_file", Content: "FRESH CONTENT FOR THE LATEST CALL"},
	}
	got := roleAndContent(ensureToolResults(in))
	want := []string{
		"assistant:",
		"tool:" + noResultStub,
		"user:[Compacted summary of the earlier conversation]",
		"assistant:",
		"tool:FRESH CONTENT FOR THE LATEST CALL",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the live call was not answered with its own result:\n got %q\nwant %q", got, want)
	}
}

// persistContextCompact cuts the persisted history at throughSeq with no
// tool-boundary rebalance (compact.go:148-149) and loadHistory rebuilds the
// tail from every later row (session.go:180-191), so a history can open with a
// tool result whose call is gone. Adopting it would answer a fresh call with
// stale output and nothing would mark it as stale.
func TestEnsureToolResults_OrphanResultIsNotAdoptedByALaterCall(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "call_0_read_file", Name: "read_file", Content: "STALE ORPHAN FROM AN EARLIER TURN"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_0_read_file", Name: "read_file"}}},
		{Role: llm.RoleTool, ToolCallID: "call_0_read_file", Name: "read_file", Content: "FRESH RESULT FOR THIS CALL"},
	}
	got := roleAndContent(ensureToolResults(in))
	want := []string{"assistant:", "tool:FRESH RESULT FOR THIS CALL"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale orphan was adopted:\n got %q\nwant %q", got, want)
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
	// A shallow copy would share the ToolCalls backing array with in, so a
	// write through msgs[i].ToolCalls[j] would land in both and go unseen.
	// A JSON round-trip detaches every field.
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var before []llm.Message
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatalf("the snapshot itself is lossy, so this test cannot detect a write:\n got %+v\nwant %+v", before, in)
	}

	ensureToolResults(in)

	if !reflect.DeepEqual(in, before) {
		t.Fatalf("input history was rewritten:\n got %+v\nwant %+v", in, before)
	}
}
