package agent

import (
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func TestRepeatKeyDistinguishesDifferentArguments(t *testing.T) {
	a := llm.ToolCall{Name: "edit_file", Arguments: `{"path":"m.go","old":"a","new":"b"}`}
	b := llm.ToolCall{Name: "edit_file", Arguments: `{"path":"m.go","old":"c","new":"d"}`}
	if repeatKey(a) == repeatKey(b) {
		t.Fatal("two different edits to one file share a fingerprint")
	}
}

func TestRepeatKeyStillCatchesAnIdenticalCall(t *testing.T) {
	a := llm.ToolCall{Name: "edit_file", Arguments: `{"path":"m.go","old":"a","new":"b"}`}
	b := llm.ToolCall{Name: "edit_file", Arguments: `{"new":"b","old":"a","path":"m.go"}`}
	if repeatKey(a) != repeatKey(b) {
		t.Fatal("the same call re-serialised was not recognised as a repeat")
	}
}

// The special cases this guard used to carry were dispatched on tool name, so
// every name is its own branch and needs its own assertion. One tool can be
// given back a fingerprint that ignores most of its arguments while every other
// tool stays uniform, and nothing about the remaining tools would notice. These
// are the two names that carried a case; edit_file is here for the third.
func TestRepeatKeyUsesFullArgumentsForEveryTool(t *testing.T) {
	for _, tc := range []struct {
		tool string
		a, b string
		what string
	}{
		{
			tool: "write_file",
			a:    `{"path":"config.yaml","content":"v1"}`,
			b:    `{"path":"config.yaml","content":"v2"}`,
			what: "two writes of different content to one path",
		},
		{
			tool: "vps_upload",
			a:    `{"remote_path":"/tmp/x","local_path":"/a/b"}`,
			b:    `{"remote_path":"/tmp/x","local_path":"/c/d"}`,
			what: "two uploads of different local files to one remote path",
		},
		{
			tool: "edit_file",
			a:    `{"path":"main.go","old_string":"a","new_string":"b"}`,
			b:    `{"path":"main.go","old_string":"c","new_string":"d"}`,
			what: "two different edits to one file",
		},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			x := llm.ToolCall{Name: tc.tool, Arguments: tc.a}
			y := llm.ToolCall{Name: tc.tool, Arguments: tc.b}
			if got := repeatKey(x); got == repeatKey(y) {
				t.Fatalf("%s share the fingerprint %q, so %s is keyed on a subset of its arguments",
					tc.what, got, tc.tool)
			}
		})
	}
}

func TestRepeatTrackerTripsOnIdenticalCallsOnly(t *testing.T) {
	r := newRepeatTracker(3)
	same := llm.ToolCall{Name: "grep", Arguments: `{"pattern":"x"}`}
	for i := 1; i <= 2; i++ {
		if tripped := r.record([]llm.ToolCall{same}); len(tripped) > 0 {
			t.Fatalf("tripped early at %d", i)
		}
	}
	if tripped := r.record([]llm.ToolCall{same}); len(tripped) == 0 {
		t.Fatal("three identical calls did not trip the guard")
	}
}
