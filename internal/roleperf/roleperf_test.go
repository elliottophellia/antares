package roleperf

import (
	"path/filepath"
	"testing"
)

func TestUntriedRoleIsNeutral(t *testing.T) {
	tr := NewTracker("")
	// A role with no missions should not be scored as bad — it is untried.
	if s, ok := tr.Get("coder"); ok {
		t.Fatalf("an untried role should not exist yet, got %+v", s)
	}
}

func TestSuccessRaisesScore(t *testing.T) {
	tr := NewTracker("")
	first := tr.Record(Outcome{Role: "coder", Success: true, Kept: true, Turns: 3})
	if first <= 50 {
		t.Fatalf("a clean, kept, efficient mission should score above neutral, got %v", first)
	}
	// Failures pull it down.
	tr.Record(Outcome{Role: "coder", Success: false})
	tr.Record(Outcome{Role: "coder", Success: false})
	s, _ := tr.Get("coder")
	if s.Score >= first {
		t.Fatalf("failures did not lower the score: %v then %v", first, s.Score)
	}
	if s.Missions != 3 || s.Successes != 1 || s.Failures != 2 {
		t.Fatalf("counts wrong: %+v", s)
	}
}

func TestEfficiencyMatters(t *testing.T) {
	tr := NewTracker("")
	tr.Record(Outcome{Role: "fast", Success: true, Kept: true, Turns: 2})
	tr.Record(Outcome{Role: "slow", Success: true, Kept: true, Turns: 40})
	fast, _ := tr.Get("fast")
	slow, _ := tr.Get("slow")
	if fast.Score <= slow.Score {
		t.Fatalf("the efficient role should score higher: fast=%v slow=%v", fast.Score, slow.Score)
	}
}

func TestListIsBestFirst(t *testing.T) {
	tr := NewTracker("")
	tr.Record(Outcome{Role: "good", Success: true, Kept: true, Turns: 3})
	tr.Record(Outcome{Role: "bad", Success: false})
	list := tr.List()
	if len(list) != 2 || list[0].Role != "good" {
		t.Fatalf("list not ordered by score: %+v", list)
	}
}

func TestPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf.json")
	tr := NewTracker(path)
	tr.Record(Outcome{Role: "coder", Success: true, Kept: true, Turns: 3})

	fresh := NewTracker(path)
	s, ok := fresh.Get("coder")
	if !ok || s.Missions != 1 {
		t.Fatalf("did not persist: %+v", s)
	}
}

func TestEmptyRoleIgnored(t *testing.T) {
	tr := NewTracker("")
	if got := tr.Record(Outcome{Role: ""}); got != 0 {
		t.Fatalf("recording a mission with no role should do nothing, got %v", got)
	}
	if len(tr.List()) != 0 {
		t.Fatal("an empty-role mission created a stats entry")
	}
}
