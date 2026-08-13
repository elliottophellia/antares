package agent

import (
	"strconv"
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

// The guard has two jobs: nudge a model that is repeating itself, and stop one
// that keeps repeating after being nudged. record only reports a key on the
// turn its count reaches the limit, so asking about the stop only when there is
// something to nudge about asks exactly once — on the turn the count is limit,
// which is half of what exceeded() needs. The stop could then only ever fire
// when a *second*, different call tripped after a first had already run past
// twice the limit; a model stuck on one call was never stopped at all.
func TestRepeatGuardStopsAModelStuckOnOneCall(t *testing.T) {
	r := newRepeatTracker(3)
	call := llm.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}

	nudges, stoppedAt := 0, 0
	for i := 1; i <= 60; i++ {
		stuck, stop := r.check([]llm.ToolCall{call})
		if len(stuck) > 0 {
			nudges++
		}
		if stop {
			stoppedAt = i
			break
		}
	}

	if stoppedAt == 0 {
		t.Fatalf("60 identical calls produced %d nudge(s) and no stop at all", nudges)
	}
	if stoppedAt != 6 {
		t.Fatalf("stopped after %d identical calls, want twice the limit of 3", stoppedAt)
	}
	if nudges != 1 {
		t.Fatalf("nudged %d times before stopping, want the one nudge at the limit", nudges)
	}
}

// The stop must not front-run the nudge: a model gets told it is repeating
// itself, and gets the turns between the limit and twice the limit to act on
// that, before the run is taken away from it.
func TestRepeatGuardNudgesBeforeItStops(t *testing.T) {
	r := newRepeatTracker(3)
	call := llm.ToolCall{Name: "grep", Arguments: `{"pattern":"x"}`}

	for i := 1; i <= 5; i++ {
		stuck, stop := r.check([]llm.ToolCall{call})
		if stop {
			t.Fatalf("stopped after %d calls, before the nudge had a chance to work", i)
		}
		if i == 3 && len(stuck) != 1 {
			t.Fatalf("the call at the limit did not nudge: %v", stuck)
		}
		if i != 3 && len(stuck) != 0 {
			t.Fatalf("call %d nudged again: %v", i, stuck)
		}
	}
	if _, stop := r.check([]llm.ToolCall{call}); !stop {
		t.Fatal("six identical calls at a limit of three did not stop the run")
	}
}

// Ordinary work must survive the same number of turns. Distinct arguments are
// distinct calls, so nothing here should ever reach the limit, let alone twice
// it — and the stop is now asked on every turn, which is where a guard keyed
// too coarsely would show up as an abort in the middle of a real task.
func TestRepeatGuardLetsDistinctWorkRun(t *testing.T) {
	r := newRepeatTracker(3)
	for i := 0; i < 60; i++ {
		stuck, stop := r.check([]llm.ToolCall{{
			Name:      "edit_file",
			Arguments: `{"path":"main.go","old":"line ` + strconv.Itoa(i) + `","new":"x"}`,
		}})
		if stop {
			t.Fatalf("distinct edits were stopped as a loop after %d calls", i+1)
		}
		if len(stuck) > 0 {
			t.Fatalf("distinct edits were nudged as a repeat after %d calls: %v", i+1, stuck)
		}
	}
}

// Polling a managed process is an observation of changing external state, and
// the guard skips it entirely. Now that the stop is evaluated every turn rather
// than only behind a nudge, a long wait must still not end the run.
func TestRepeatGuardDoesNotStopManagedProcessPolling(t *testing.T) {
	r := newRepeatTracker(3)
	call := llm.ToolCall{Name: "process", Arguments: `{"action":"wait","process_id":"proc_1","timeout":30}`}
	for i := 0; i < 60; i++ {
		if _, stop := r.check([]llm.ToolCall{call}); stop {
			t.Fatalf("waiting on a managed process was stopped as a loop after %d polls", i+1)
		}
	}
}
