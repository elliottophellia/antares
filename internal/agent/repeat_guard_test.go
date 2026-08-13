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
