// Package findings records what a security engagement turned up.
//
// A finding discovered and not written down is a finding lost. This is the
// ledger the security roles write to as they work and the report role reads
// from at the end — the connective tissue between testing and the report.
package findings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Severity ranks a finding. The order matters: a report leads with the worst.
type Severity string

const (
	Critical Severity = "critical"
	High     Severity = "high"
	Medium   Severity = "medium"
	Low      Severity = "low"
	Info     Severity = "info"
)

func severityRank(s Severity) int {
	switch s {
	case Critical:
		return 0
	case High:
		return 1
	case Medium:
		return 2
	case Low:
		return 3
	default:
		return 4
	}
}

// NormalizeSeverity maps free text to a known level, defaulting to info so an
// odd value never silently becomes critical.
func NormalizeSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "crit":
		return Critical
	case "high":
		return High
	case "medium", "med", "moderate":
		return Medium
	case "low":
		return Low
	default:
		return Info
	}
}

// Finding is one recorded issue.
type Finding struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Severity    Severity `json:"severity"`
	Target      string   `json:"target,omitempty"`
	Description string   `json:"description"`
	// Reproduce is the exact steps someone can follow to see it.
	Reproduce string `json:"reproduce,omitempty"`
	// Impact is what an attacker gains.
	Impact string `json:"impact,omitempty"`
	// Remediation is the concrete fix.
	Remediation string `json:"remediation,omitempty"`
	// CWE is an optional classification, e.g. "CWE-89".
	CWE string `json:"cwe,omitempty"`
	// Endpoint is the specific URL/parameter/API route, finer than Target.
	Endpoint string `json:"endpoint,omitempty"`
	// AttackVector is how it is reached, e.g. "network", "authenticated user".
	AttackVector string `json:"attack_vector,omitempty"`
	// PoC is a proof-of-concept: a request, payload, or snippet that shows it.
	PoC string `json:"poc,omitempty"`
	// Status tracks triage: new, confirmed, duplicate, or wontfix.
	Status Status `json:"status,omitempty"`
	// DuplicateOf points at the earlier finding this one repeats, when Status
	// is duplicate.
	DuplicateOf string    `json:"duplicate_of,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Status is where a finding sits in triage.
type Status string

const (
	StatusNew       Status = "new"
	StatusConfirmed Status = "confirmed"
	StatusDuplicate Status = "duplicate"
	StatusWontfix   Status = "wontfix"
)

// NormalizeStatus maps free text onto a known status, defaulting to new.
func NormalizeStatus(s string) Status {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "confirmed", "confirm", "valid", "approved":
		return StatusConfirmed
	case "duplicate", "dup":
		return StatusDuplicate
	case "wontfix", "won't fix", "ignore", "rejected":
		return StatusWontfix
	default:
		return StatusNew
	}
}

// Store persists findings per session as one JSON file each.
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore roots a store at dir.
func NewStore(dir string) *Store { return &Store{root: dir} }

func (s *Store) path(sessionID string) string {
	return filepath.Join(s.root, safeName(sessionID)+".json")
}

// Add records a finding and returns it with its assigned id and time.
func (s *Store) Add(sessionID string, f Finding) (Finding, error) {
	if sessionID == "" {
		return f, fmt.Errorf("a finding needs a session to belong to")
	}
	if strings.TrimSpace(f.Title) == "" {
		return f, fmt.Errorf("a finding needs a title")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load(sessionID)
	if err != nil {
		return f, err
	}
	f.ID = fmt.Sprintf("F-%03d", len(list)+1)
	if f.CreatedAt.IsZero() {
		// Callers stamp this; a zero value only happens in a direct test.
		f.CreatedAt = time.Now()
	}
	if f.Severity == "" {
		f.Severity = Info
	}
	if f.Status == "" {
		f.Status = StatusNew
	}
	// A finding is never dropped, but one that repeats an earlier one is flagged
	// so the report is not padded with the same issue twice.
	if dup := findSimilar(list, f); dup != "" {
		f.Status = StatusDuplicate
		f.DuplicateOf = dup
	}
	list = append(list, f)
	return f, s.save(sessionID, list)
}

// findSimilar returns the id of an existing finding that looks like the same
// issue — same normalised title on the same target — or "".
func findSimilar(list []Finding, f Finding) string {
	nt, tg := normaliseTitle(f.Title), strings.ToLower(strings.TrimSpace(f.Target))
	for _, e := range list {
		if e.Status == StatusDuplicate {
			continue
		}
		if normaliseTitle(e.Title) == nt && strings.ToLower(strings.TrimSpace(e.Target)) == tg {
			return e.ID
		}
	}
	return ""
}

func normaliseTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// Triage sets a finding's status. Marking one confirmed clears any duplicate
// link; marking it duplicate needs the id it duplicates.
func (s *Store) Triage(sessionID, id string, status Status, duplicateOf string) (Finding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(sessionID)
	if err != nil {
		return Finding{}, false, err
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		list[i].Status = status
		if status == StatusDuplicate {
			list[i].DuplicateOf = duplicateOf
		} else {
			list[i].DuplicateOf = ""
		}
		return list[i], true, s.save(sessionID, list)
	}
	return Finding{}, false, nil
}

// List returns a session's findings, worst first.
func (s *Store) List(sessionID string) ([]Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(sessionID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		return severityRank(list[i].Severity) < severityRank(list[j].Severity)
	})
	return list, nil
}

// Remove deletes one finding by id.
func (s *Store) Remove(sessionID, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(sessionID)
	if err != nil {
		return false, err
	}
	kept := list[:0]
	found := false
	for _, f := range list {
		if f.ID == id {
			found = true
			continue
		}
		kept = append(kept, f)
	}
	if !found {
		return false, nil
	}
	return true, s.save(sessionID, kept)
}

// Clear forgets a session's findings.
func (s *Store) Clear(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Remove(s.path(sessionID))
}

func (s *Store) load(sessionID string) ([]Finding, error) {
	raw, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []Finding
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) save(sessionID string, list []Finding) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	// Write-then-rename, so an interrupted save cannot corrupt the ledger.
	tmp := s.path(sessionID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(sessionID))
}

// Report renders a session's findings as a Markdown report body.
func (s *Store) Report(sessionID, title string) (string, error) {
	list, err := s.List(sessionID)
	if err != nil {
		return "", err
	}
	if title == "" {
		title = "Security Assessment"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)

	if len(list) == 0 {
		b.WriteString("No findings were recorded.\n")
		return b.String(), nil
	}

	// A summary count by severity, worst first.
	counts := map[Severity]int{}
	for _, f := range list {
		counts[f.Severity]++
	}
	b.WriteString("## Summary\n\n")
	for _, sev := range []Severity{Critical, High, Medium, Low, Info} {
		if counts[sev] > 0 {
			fmt.Fprintf(&b, "- **%s**: %d\n", titleCase(string(sev)), counts[sev])
		}
	}
	b.WriteString("\n## Findings\n")

	for _, f := range list {
		// Duplicates and won't-fixes are listed compactly at the end, not given
		// a full write-up that repeats an issue already covered.
		if f.Status == StatusDuplicate || f.Status == StatusWontfix {
			continue
		}
		fmt.Fprintf(&b, "\n### %s — %s (%s)\n\n", f.ID, f.Title, titleCase(string(f.Severity)))
		if f.Target != "" {
			fmt.Fprintf(&b, "**Target:** `%s`  \n", f.Target)
		}
		if f.Endpoint != "" {
			fmt.Fprintf(&b, "**Endpoint:** `%s`  \n", f.Endpoint)
		}
		if f.CWE != "" {
			fmt.Fprintf(&b, "**Classification:** %s  \n", f.CWE)
		}
		if f.AttackVector != "" {
			fmt.Fprintf(&b, "**Attack vector:** %s  \n", f.AttackVector)
		}
		if f.Description != "" {
			fmt.Fprintf(&b, "\n%s\n", f.Description)
		}
		if f.Reproduce != "" {
			fmt.Fprintf(&b, "\n**Steps to reproduce**\n\n%s\n", f.Reproduce)
		}
		if f.PoC != "" {
			fmt.Fprintf(&b, "\n**Proof of concept**\n\n```\n%s\n```\n", f.PoC)
		}
		if f.Impact != "" {
			fmt.Fprintf(&b, "\n**Impact:** %s\n", f.Impact)
		}
		if f.Remediation != "" {
			fmt.Fprintf(&b, "\n**Remediation:** %s\n", f.Remediation)
		}
	}

	// A short trailer records what was set aside, so nothing looks lost.
	var deferred []Finding
	for _, f := range list {
		if f.Status == StatusDuplicate || f.Status == StatusWontfix {
			deferred = append(deferred, f)
		}
	}
	if len(deferred) > 0 {
		b.WriteString("\n## Not reported\n\n")
		for _, f := range deferred {
			note := string(f.Status)
			if f.Status == StatusDuplicate && f.DuplicateOf != "" {
				note = "duplicate of " + f.DuplicateOf
			}
			fmt.Fprintf(&b, "- %s — %s (%s)\n", f.ID, f.Title, note)
		}
	}
	return b.String(), nil
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func safeName(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
	if out == "" {
		return "unknown"
	}
	return out
}
