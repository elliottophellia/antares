package engagement

import (
	"testing"
)

func TestAddDeduplicates(t *testing.T) {
	s := NewStore(t.TempDir())
	a, added, err := s.Add("sess", Intel{Type: IntelEndpoint, Value: "/api/login"})
	if err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	if a.ID != "I-001" {
		t.Fatalf("id = %q", a.ID)
	}
	// The same endpoint again is not a new fact.
	b, added, _ := s.Add("sess", Intel{Type: IntelEndpoint, Value: "/API/login"})
	if added {
		t.Fatal("a duplicate endpoint was recorded as new")
	}
	if b.ID != a.ID {
		t.Fatal("the existing entry was not returned")
	}
	// A different type with the same value is a different fact.
	if _, added, _ := s.Add("sess", Intel{Type: IntelNote, Value: "/api/login"}); !added {
		t.Fatal("a different type was treated as a duplicate")
	}
}

func TestAddRequiresValueAndSession(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Add("", Intel{Type: IntelHost, Value: "x"}); err == nil {
		t.Fatal("intel without a session was accepted")
	}
	if _, _, err := s.Add("s", Intel{Type: IntelHost, Value: " "}); err == nil {
		t.Fatal("intel without a value was accepted")
	}
}

func TestStateProgressesWithEvidence(t *testing.T) {
	s := NewStore(t.TempDir())

	// Nothing recorded, no scope: every phase is not started.
	states, err := s.State("s", false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if st.Status != NotStarted {
			t.Fatalf("%s should be not_started, got %s", st.Name, st.Status)
		}
	}

	// Scope authorized, and a host found: scope and recon complete, the rest
	// still waiting.
	_, _, _ = s.Add("s", Intel{Type: IntelHost, Value: "app.example.com"})
	states, _ = s.State("s", true, false)
	byName := map[string]PhaseState{}
	for _, st := range states {
		byName[st.Name] = st
	}
	if byName["scope"].Status != Complete {
		t.Fatalf("scope = %s", byName["scope"].Status)
	}
	if byName["recon"].Status != Complete {
		t.Fatalf("recon = %s", byName["recon"].Status)
	}
	if byName["testing"].Status != NotStarted {
		t.Fatalf("testing = %s, should not have started", byName["testing"].Status)
	}
}

func TestSkippedPhaseIsBlocked(t *testing.T) {
	s := NewStore(t.TempDir())
	// A vulnerability recorded with no enumeration before it: the testing
	// phase has evidence but a prerequisite was skipped.
	_, _, _ = s.Add("s", Intel{Type: IntelVulnerability, Value: "SQLi in /search"})
	states, _ := s.State("s", true, false)

	byName := map[string]PhaseState{}
	for _, st := range states {
		byName[st.Name] = st
	}
	if byName["testing"].Status != Blocked {
		t.Fatalf("testing with skipped prerequisites should be blocked, got %s", byName["testing"].Status)
	}
	// Enumeration was skipped before a later phase produced evidence, so it is
	// in progress, not untouched.
	if byName["enumeration"].Status != InProgress {
		t.Fatalf("enumeration = %s, expected in_progress", byName["enumeration"].Status)
	}
}

func TestNextStep(t *testing.T) {
	s := NewStore(t.TempDir())
	states, _ := s.State("s", false, false)
	// With nothing done, the first phase is next.
	next, directive := NextStep(states)
	if next.Name != "scope" {
		t.Fatalf("next = %s", next.Name)
	}
	if directive == "" {
		t.Fatal("a next step with no directive is not useful")
	}

	// With everything present, the directive points at reporting.
	_, _, _ = s.Add("s", Intel{Type: IntelHost, Value: "h"})
	_, _, _ = s.Add("s", Intel{Type: IntelEndpoint, Value: "/e"})
	_, _, _ = s.Add("s", Intel{Type: IntelVulnerability, Value: "v"})
	states, _ = s.State("s", true, true)
	_, directive = NextStep(states)
	if directive == "" {
		t.Fatal("expected a closing directive")
	}
}

func TestNormalizeType(t *testing.T) {
	cases := map[string]IntelType{
		"HOST": IntelHost, "domain": IntelHost, "url": IntelEndpoint,
		"tech": IntelTechnology, "vuln": IntelVulnerability, "": IntelNote,
		"something-odd": IntelNote, "subdomain": IntelSubdomain,
	}
	for in, want := range cases {
		if got := NormalizeType(in); got != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPersistsAcrossStores(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	_, _, _ = s1.Add("s", Intel{Type: IntelHost, Value: "kept.example.com"})

	s2 := NewStore(dir)
	list, err := s2.List("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Value != "kept.example.com" {
		t.Fatalf("did not persist: %+v", list)
	}
}
