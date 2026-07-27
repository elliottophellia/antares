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
	CWE       string    `json:"cwe,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
	list = append(list, f)
	return f, s.save(sessionID, list)
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
		fmt.Fprintf(&b, "\n### %s — %s (%s)\n\n", f.ID, f.Title, titleCase(string(f.Severity)))
		if f.Target != "" {
			fmt.Fprintf(&b, "**Target:** `%s`  \n", f.Target)
		}
		if f.CWE != "" {
			fmt.Fprintf(&b, "**Classification:** %s  \n", f.CWE)
		}
		if f.Description != "" {
			fmt.Fprintf(&b, "\n%s\n", f.Description)
		}
		if f.Reproduce != "" {
			fmt.Fprintf(&b, "\n**Steps to reproduce**\n\n%s\n", f.Reproduce)
		}
		if f.Impact != "" {
			fmt.Fprintf(&b, "\n**Impact:** %s\n", f.Impact)
		}
		if f.Remediation != "" {
			fmt.Fprintf(&b, "\n**Remediation:** %s\n", f.Remediation)
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
