package engagement

import "strings"

// A single vulnerability is one thing; two that combine are often much worse.
// An IDOR next to an authentication bypass is account takeover; an SSRF next to
// a cloud metadata endpoint is credential theft. Chain detection looks across
// the confirmed findings for these dangerous combinations and names them, so
// the report reflects the real impact rather than a list of media-severity
// issues that together are critical.

// ChainRule is a pattern of two or more weaknesses that compound.
type ChainRule struct {
	Name string
	// Each element is a set of keywords; the rule matches when every element
	// has at least one keyword present somewhere in the evidence.
	Parts [][]string
	// Impact is the combined consequence, for the report.
	Impact string
}

// ChainRules are the combinations worth flagging. Keywords match finding
// titles, CWEs, and descriptions case-insensitively.
var ChainRules = []ChainRule{
	{
		Name:   "Account takeover",
		Parts:  [][]string{{"idor", "cwe-639", "broken object", "bola"}, {"auth", "session", "password reset", "jwt", "cwe-287", "cwe-640"}},
		Impact: "An access-control flaw combined with an authentication weakness lets an attacker take over other users' accounts.",
	},
	{
		Name:   "Cloud credential theft via SSRF",
		Parts:  [][]string{{"ssrf", "cwe-918", "request forgery"}, {"metadata", "169.254.169.254", "imds", "cloud", "instance credential"}},
		Impact: "An SSRF that can reach the cloud metadata service yields the workload's credentials and, from there, the account.",
	},
	{
		Name:   "Authenticated RCE",
		Parts:  [][]string{{"auth bypass", "weak auth", "default credential", "cwe-287", "cwe-1391"}, {"rce", "command injection", "deserial", "file upload", "cwe-77", "cwe-78", "cwe-94", "cwe-502"}},
		Impact: "An authentication weakness plus a code-execution primitive gives an attacker a shell as the application.",
	},
	{
		Name:   "Stored XSS to session hijack",
		Parts:  [][]string{{"stored xss", "persistent xss", "cwe-79"}, {"session", "cookie", "httponly", "token"}},
		Impact: "Stored XSS with reachable session material lets an attacker ride authenticated sessions.",
	},
	{
		Name:   "Privilege escalation to admin",
		Parts:  [][]string{{"privilege", "escalation", "cwe-269", "mass assignment", "cwe-915"}, {"admin", "role", "authorization", "cwe-285", "cwe-863"}},
		Impact: "A privilege-handling flaw next to weak authorization elevates a normal user to administrator.",
	},
	{
		Name:   "Exposed secret to lateral movement",
		Parts:  [][]string{{"secret", "api key", "credential", "token", "cwe-200", "cwe-312"}, {"reuse", "lateral", "internal", "pivot", "access"}},
		Impact: "A leaked secret that is accepted elsewhere lets an attacker move deeper into the environment.",
	},
}

// Chain is a detected combination.
type Chain struct {
	Name   string `json:"name"`
	Impact string `json:"impact"`
}

// DetectChains returns the chains whose every part is present in the evidence.
func DetectChains(evidence []string) []Chain {
	hay := strings.ToLower(strings.Join(evidence, " \n "))
	var out []Chain
	for _, rule := range ChainRules {
		if allPartsPresent(hay, rule.Parts) {
			out = append(out, Chain{Name: rule.Name, Impact: rule.Impact})
		}
	}
	return out
}

func allPartsPresent(hay string, parts [][]string) bool {
	for _, part := range parts {
		found := false
		for _, kw := range part {
			if strings.Contains(hay, kw) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
