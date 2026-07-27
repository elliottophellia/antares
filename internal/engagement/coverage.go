package engagement

import "strings"

// The testing phase is one box on the phase board, but "test each surface" hides
// a checklist: authentication, access control, injection, and the rest. Without
// it an agent tests the one class it thought of and calls the phase done. The
// coverage areas make that checklist explicit and track which areas have
// produced evidence, so "you have not tested access control" can be said out
// loud.

// TestingArea is one category of the vulnerability-testing phase.
type TestingArea struct {
	Name     string
	Title    string
	Keywords []string
}

// TestingAreas is the coverage checklist, drawn from the common web/app testing
// categories (OWASP WSTG / Top 10). It is a guide, not a gate.
var TestingAreas = []TestingArea{
	{"authentication", "Authentication", []string{"auth", "login", "password", "mfa", "2fa", "session", "jwt", "credential", "cwe-287", "cwe-384", "cwe-613"}},
	{"access-control", "Access Control / Authorization", []string{"authorization", "access control", "idor", "privilege", "escalation", "bola", "bfla", "cwe-285", "cwe-639", "cwe-863", "cwe-284"}},
	{"injection", "Injection", []string{"injection", "sqli", "sql", "xss", "command", "template", "ssti", "ldap", "nosql", "xxe", "cwe-89", "cwe-79", "cwe-77", "cwe-78", "cwe-94", "cwe-611"}},
	{"ssrf", "SSRF & Request Forgery", []string{"ssrf", "request forgery", "csrf", "redirect", "cwe-918", "cwe-352", "cwe-601"}},
	{"business-logic", "Business Logic", []string{"business logic", "logic flaw", "race condition", "workflow", "mass assignment", "cwe-840", "cwe-841", "cwe-362"}},
	{"data-exposure", "Sensitive Data Exposure", []string{"exposure", "disclosure", "leak", "sensitive", "secret", "pii", "cwe-200", "cwe-538", "cwe-312"}},
	{"configuration", "Security Misconfiguration", []string{"misconfig", "configuration", "header", "cors", "tls", "default", "verbose error", "cwe-16", "cwe-693", "cwe-756"}},
	{"components", "Vulnerable Components", []string{"outdated", "vulnerable component", "dependency", "known cve", "version", "cwe-1104", "cwe-937"}},
}

// AreaStatus pairs a testing area with whether it has evidence yet.
type AreaStatus struct {
	Area    TestingArea
	Covered bool
}

// Coverage marks each testing area covered when any evidence string mentions one
// of its keywords. Evidence is typically finding titles, CWEs, and intel values.
func Coverage(evidence []string) []AreaStatus {
	hay := strings.ToLower(strings.Join(evidence, " \n "))
	out := make([]AreaStatus, 0, len(TestingAreas))
	for _, area := range TestingAreas {
		covered := false
		for _, kw := range area.Keywords {
			if strings.Contains(hay, kw) {
				covered = true
				break
			}
		}
		out = append(out, AreaStatus{Area: area, Covered: covered})
	}
	return out
}

// CoveragePercent is the share of testing areas with evidence, 0–100.
func CoveragePercent(states []AreaStatus) int {
	if len(states) == 0 {
		return 0
	}
	n := 0
	for _, s := range states {
		if s.Covered {
			n++
		}
	}
	return n * 100 / len(states)
}
