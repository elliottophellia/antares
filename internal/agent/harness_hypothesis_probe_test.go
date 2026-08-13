package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/llm"
)

// A user message interleaved between an assistant's tool_calls and their
// results is exactly what the repetition guard produces: it appends its nudge
// to history before executeTools appends the results. The real results must
// still reach the model.
func TestProbeInterleavedNudgeKeepsRealToolResults(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "read the file"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		}},
		{Role: llm.RoleUser, Content: "You have called read_file with the same arguments several times."},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "read_file", Content: "REAL FILE CONTENT"},
	}

	out := ensureToolResults(history)

	var got string
	for _, m := range out {
		if m.Role == llm.RoleTool && m.ToolCallID == "c1" {
			got = m.Content
		}
	}
	if got != "REAL FILE CONTENT" {
		t.Fatalf("tool result for c1 = %q, want %q", got, "REAL FILE CONTENT")
	}
}

// Tool output handed to the model must stay valid UTF-8 no matter where the
// truncation boundary lands.
func TestProbeTrimForModelKeepsValidUTF8(t *testing.T) {
	content := strings.Repeat("é", 100) // 200 bytes, 2 bytes per rune
	got := trimForModel(content, 51)
	if !utf8.ValidString(got) {
		t.Fatalf("trimForModel produced invalid UTF-8: %q", got)
	}
}

// Running a destructive command on a remote host is no less destructive than
// running it locally.
func TestProbeDangerScanCoversRemoteExecution(t *testing.T) {
	why := dangerIn("vps_run", `{"command":"rm -rf / --no-preserve-root"}`)
	if why == "" {
		t.Fatalf("vps_run with a root wipe was not classified as dangerous")
	}
}

// Deleting a user's entire home directory is destructive even though the path
// does not stop at the first slash.
func TestProbeDangerScanCatchesHomeDirectoryWipe(t *testing.T) {
	why := dangerIn("terminal", `{"command":"rm -rf /home/nvdorman"}`)
	if why == "" {
		t.Fatalf("rm -rf /home/nvdorman was not classified as dangerous")
	}
}

// Editing the same file several times in one turn is ordinary work, not a
// stuck model repeating an identical call.
func TestProbeDistinctEditsToSameFileAreNotRepeats(t *testing.T) {
	r := newRepeatTracker(3)
	calls := []llm.ToolCall{
		{Name: "edit_file", Arguments: `{"path":"main.go","old":"import a","new":"import b"}`},
		{Name: "edit_file", Arguments: `{"path":"main.go","old":"func x","new":"func y"}`},
		{Name: "edit_file", Arguments: `{"path":"main.go","old":"return 1","new":"return 2"}`},
	}
	for i, c := range calls {
		if tripped := r.record([]llm.ToolCall{c}); len(tripped) > 0 {
			t.Fatalf("edit %d to the same file flagged as a repeat: %v", i+1, tripped)
		}
	}
}
