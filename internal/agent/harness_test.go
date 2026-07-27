package agent

import (
	"context"
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func TestRepeatTrackerTripsOnIdenticalCalls(t *testing.T) {
	r := newRepeatTracker(3)
	call := llm.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}

	if got := r.record([]llm.ToolCall{call}); len(got) != 0 {
		t.Fatalf("first call tripped: %v", got)
	}
	if got := r.record([]llm.ToolCall{call}); len(got) != 0 {
		t.Fatalf("second call tripped: %v", got)
	}
	got := r.record([]llm.ToolCall{call})
	if len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("third identical call should trip, got %v", got)
	}
	// It must fire once, not on every call after the limit, or the history
	// fills with the same nudge.
	if again := r.record([]llm.ToolCall{call}); len(again) != 0 {
		t.Fatalf("the nudge repeated: %v", again)
	}
}

func TestRepeatTrackerIgnoresDifferentArguments(t *testing.T) {
	r := newRepeatTracker(2)
	for _, path := range []string{"a", "b", "c", "d"} {
		got := r.record([]llm.ToolCall{{Name: "read_file", Arguments: `{"path":"` + path + `"}`}})
		if len(got) != 0 {
			t.Fatalf("reading %q tripped the guard: %v", path, got)
		}
	}
}

func TestRepeatTrackerNormalisesArguments(t *testing.T) {
	r := newRepeatTracker(2)
	// Same call, re-serialised with different key order and spacing.
	r.record([]llm.ToolCall{{Name: "grep", Arguments: `{"pattern":"x","path":"."}`}})
	got := r.record([]llm.ToolCall{{Name: "grep", Arguments: `{ "path": ".", "pattern": "x" }`}})
	if len(got) != 1 {
		t.Fatalf("a re-serialised identical call was not recognised: %v", got)
	}
}

func TestRepeatTrackerExceeded(t *testing.T) {
	r := newRepeatTracker(2)
	call := llm.ToolCall{Name: "terminal", Arguments: `{"command":"ls"}`}
	for i := 0; i < 3; i++ {
		r.record([]llm.ToolCall{call})
		if r.exceeded() {
			t.Fatalf("gave up after %d calls, too early", i+1)
		}
	}
	r.record([]llm.ToolCall{call})
	if !r.exceeded() {
		t.Fatal("expected the run to be abandoned after twice the limit")
	}
}

func TestSteeringRequiresARunningSession(t *testing.T) {
	a := &Agent{active: map[string]context.CancelFunc{}}
	if a.Steer("nope", "do this instead") {
		t.Fatal("steering a session that is not running should report false")
	}

	a.active["s1"] = func() {}
	if !a.Steer("s1", "do this instead") {
		t.Fatal("steering a running session should be accepted")
	}
	if a.Steer("s1", "   ") {
		t.Fatal("an empty note should be rejected")
	}

	notes := drainSteering("s1")
	if len(notes) != 1 || notes[0] != "do this instead" {
		t.Fatalf("got %v", notes)
	}
	// Draining takes them, so a later turn does not replay old instructions.
	if again := drainSteering("s1"); len(again) != 0 {
		t.Fatalf("notes were replayed: %v", again)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`{"complete":true}`:                                 `{"complete":true}`,
		"```json\n{\"complete\":false}\n```":                `{"complete":false}`,
		"Here is my verdict:\n{\"complete\": true}\nThanks": `{"complete": true}`,
		"no json here":                                      "no json here",
	}
	for in, want := range cases {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormaliseArgsToleratesGarbage(t *testing.T) {
	// A model sometimes emits arguments that are not valid JSON at all; the
	// guard must still fingerprint them rather than panic.
	if got := normaliseArgs(`{"broken":`); got != `{"broken":` {
		t.Fatalf("got %q", got)
	}
}
