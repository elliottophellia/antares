package engagement

import "testing"

func TestCoverageMatchesKeywords(t *testing.T) {
	states := Coverage([]string{"Reflected XSS in search", "CWE-79"})
	byName := map[string]bool{}
	for _, s := range states {
		byName[s.Area.Name] = s.Covered
	}
	if !byName["injection"] {
		t.Fatal("XSS/CWE-79 should cover the injection area")
	}
	if byName["business-logic"] {
		t.Fatal("nothing here should cover business logic")
	}
}

func TestCoveragePercent(t *testing.T) {
	if got := CoveragePercent(Coverage(nil)); got != 0 {
		t.Fatalf("no evidence should be 0%%, got %d", got)
	}
	// One area covered out of len(TestingAreas).
	states := Coverage([]string{"IDOR privilege escalation"})
	pct := CoveragePercent(states)
	if pct <= 0 || pct >= 100 {
		t.Fatalf("one covered area should be a partial percent, got %d", pct)
	}
}
