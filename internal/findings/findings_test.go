package findings

import (
	"strings"
	"testing"
)

func TestAddAssignsIdsInOrder(t *testing.T) {
	s := NewStore(t.TempDir())
	a, err := s.Add("sess1", Finding{Title: "SQLi", Severity: High})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "F-001" {
		t.Fatalf("first id = %q", a.ID)
	}
	b, _ := s.Add("sess1", Finding{Title: "XSS", Severity: Medium})
	if b.ID != "F-002" {
		t.Fatalf("second id = %q", b.ID)
	}
}

func TestListIsWorstFirst(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Add("sess1", Finding{Title: "low", Severity: Low})
	_, _ = s.Add("sess1", Finding{Title: "crit", Severity: Critical})
	_, _ = s.Add("sess1", Finding{Title: "med", Severity: Medium})

	list, err := s.List("sess1")
	if err != nil {
		t.Fatal(err)
	}
	got := []Severity{list[0].Severity, list[1].Severity, list[2].Severity}
	want := []Severity{Critical, Medium, Low}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestSessionsAreSeparate(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Add("a", Finding{Title: "one", Severity: High})
	_, _ = s.Add("b", Finding{Title: "two", Severity: High})

	la, _ := s.List("a")
	lb, _ := s.List("b")
	if len(la) != 1 || len(lb) != 1 {
		t.Fatalf("a has %d, b has %d", len(la), len(lb))
	}
	if la[0].Title != "one" || lb[0].Title != "two" {
		t.Fatal("findings leaked between engagements")
	}
}

func TestAddRequiresTitleAndSession(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Add("", Finding{Title: "x"}); err == nil {
		t.Fatal("a finding without a session was accepted")
	}
	if _, err := s.Add("s", Finding{Title: "  "}); err == nil {
		t.Fatal("a finding without a title was accepted")
	}
}

func TestNormalizeSeverity(t *testing.T) {
	cases := map[string]Severity{
		"CRITICAL": Critical, "High": High, "moderate": Medium,
		"low": Low, "": Info, "nonsense": Info, "crit": Critical,
	}
	for in, want := range cases {
		if got := NormalizeSeverity(in); got != want {
			t.Errorf("NormalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemove(t *testing.T) {
	s := NewStore(t.TempDir())
	a, _ := s.Add("s", Finding{Title: "one", Severity: High})
	_, _ = s.Add("s", Finding{Title: "two", Severity: Low})

	ok, err := s.Remove("s", a.ID)
	if err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	list, _ := s.List("s")
	if len(list) != 1 || list[0].Title != "two" {
		t.Fatalf("after remove: %+v", list)
	}
	// Removing something that is not there reports false, not an error.
	if ok, _ := s.Remove("s", "F-999"); ok {
		t.Fatal("removing an unknown id reported success")
	}
}

func TestReportRendersBySeverity(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Add("s", Finding{
		Title: "SQL injection in search", Severity: Critical, Target: "app.example.com",
		Description: "The q parameter is concatenated into a query.",
		Reproduce:   "GET /search?q=' OR 1=1--", Impact: "Full database read.",
		Remediation: "Use parameterised queries.", CWE: "CWE-89",
	})
	_, _ = s.Add("s", Finding{Title: "Verbose errors", Severity: Low})

	report, err := s.Report("s", "Example Engagement")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Example Engagement",
		"## Summary",
		"**Critical**: 1",
		"**Low**: 1",
		"F-001 — SQL injection in search (Critical)",
		"CWE-89",
		"parameterised queries",
		"' OR 1=1",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report is missing %q:\n%s", want, report)
		}
	}
	// Critical must appear before the low finding.
	if strings.Index(report, "SQL injection") > strings.Index(report, "Verbose errors") {
		t.Fatal("the report did not order findings worst-first")
	}
}

func TestReportOnEmptyEngagement(t *testing.T) {
	s := NewStore(t.TempDir())
	report, err := s.Report("s", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "No findings") {
		t.Fatalf("got %q", report)
	}
}

func TestPersistsAcrossStores(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	_, _ = s1.Add("s", Finding{Title: "persisted", Severity: High})

	// A fresh store over the same directory sees it.
	s2 := NewStore(dir)
	list, err := s2.List("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "persisted" {
		t.Fatalf("did not persist: %+v", list)
	}
}

func TestDedupFlagsDuplicate(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Add("sess", Finding{Title: "SQL injection", Target: "example.com", Severity: High})
	dup, err := s.Add("sess", Finding{Title: "sql  injection", Target: "EXAMPLE.COM", Severity: High})
	if err != nil {
		t.Fatal(err)
	}
	if dup.Status != StatusDuplicate || dup.DuplicateOf != "F-001" {
		t.Fatalf("expected duplicate of F-001, got status=%s dup=%s", dup.Status, dup.DuplicateOf)
	}
	// A different target is not a duplicate.
	other, _ := s.Add("sess", Finding{Title: "SQL injection", Target: "other.com", Severity: High})
	if other.Status == StatusDuplicate {
		t.Fatal("a different target should not be a duplicate")
	}
}

func TestTriageChangesStatus(t *testing.T) {
	s := NewStore(t.TempDir())
	f, _ := s.Add("sess", Finding{Title: "XSS", Target: "example.com", Severity: Medium})
	got, ok, err := s.Triage("sess", f.ID, StatusConfirmed, "")
	if err != nil || !ok || got.Status != StatusConfirmed {
		t.Fatalf("expected confirmed, got ok=%v status=%s err=%v", ok, got.Status, err)
	}
	if _, ok, _ := s.Triage("sess", "F-999", StatusConfirmed, ""); ok {
		t.Fatal("triaging an unknown id should report not found")
	}
}

func TestReportExcludesDuplicates(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Add("sess", Finding{Title: "SQLi", Target: "a.com", Severity: High, Description: "real one"})
	_, _ = s.Add("sess", Finding{Title: "SQLi", Target: "a.com", Severity: High, Description: "dupe"})
	report, err := s.Report("sess", "Test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "F-001") {
		t.Fatal("the real finding should be in the report")
	}
	if !strings.Contains(report, "Not reported") || !strings.Contains(report, "duplicate of F-001") {
		t.Fatalf("the duplicate should be listed under Not reported:\n%s", report)
	}
}
