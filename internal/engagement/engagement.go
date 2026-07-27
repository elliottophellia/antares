package engagement

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

// IntelType classifies a discovered fact.
type IntelType string

const (
	IntelScope         IntelType = "scope"
	IntelHost          IntelType = "host"
	IntelSubdomain     IntelType = "subdomain"
	IntelService       IntelType = "service"
	IntelEndpoint      IntelType = "endpoint"
	IntelTechnology    IntelType = "technology"
	IntelCredential    IntelType = "credential"
	IntelParameter     IntelType = "parameter"
	IntelVulnerability IntelType = "vulnerability"
	IntelReport        IntelType = "report"
	IntelNote          IntelType = "note"
)

// KnownTypes lists the intel types a tool should offer.
var KnownTypes = []IntelType{
	IntelHost, IntelSubdomain, IntelService, IntelEndpoint, IntelTechnology,
	IntelCredential, IntelParameter, IntelVulnerability, IntelNote,
}

// NormalizeType maps free text to a known type, defaulting to a note so an odd
// value is still recorded rather than dropped.
func NormalizeType(s string) IntelType {
	t := IntelType(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range append(KnownTypes, IntelScope, IntelReport) {
		if t == known {
			return t
		}
	}
	// A couple of common synonyms.
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "domain", "ip", "address":
		return IntelHost
	case "url", "route", "path":
		return IntelEndpoint
	case "tech", "stack", "framework":
		return IntelTechnology
	case "vuln", "finding", "issue":
		return IntelVulnerability
	}
	return IntelNote
}

// Intel is one discovered fact.
type Intel struct {
	ID        string    `json:"id"`
	Type      IntelType `json:"type"`
	Value     string    `json:"value"`
	Detail    string    `json:"detail,omitempty"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store persists intel per session as one JSON file each.
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore roots a store at dir.
func NewStore(dir string) *Store { return &Store{root: dir} }

func (s *Store) path(sessionID string) string {
	return filepath.Join(s.root, safeName(sessionID)+".json")
}

// Add records a fact, deduplicating by type and value so re-recording the same
// endpoint does not inflate the coverage counts.
func (s *Store) Add(sessionID string, in Intel) (Intel, bool, error) {
	if sessionID == "" {
		return in, false, fmt.Errorf("intel needs a session to belong to")
	}
	if strings.TrimSpace(in.Value) == "" {
		return in, false, fmt.Errorf("intel needs a value")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.load(sessionID)
	if err != nil {
		return in, false, err
	}
	for _, existing := range list {
		if existing.Type == in.Type && strings.EqualFold(existing.Value, in.Value) {
			return existing, false, nil // already known
		}
	}
	in.ID = fmt.Sprintf("I-%03d", len(list)+1)
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	if in.Type == "" {
		in.Type = IntelNote
	}
	list = append(list, in)
	return in, true, s.save(sessionID, list)
}

// List returns a session's intel, newest first.
func (s *Store) List(sessionID string) ([]Intel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load(sessionID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list, nil
}

// Clear forgets a session's intel.
func (s *Store) Clear(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(sessionID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// State computes the phase progress for a session. hasScope and hasReport come
// from outside the intel ledger: scope is configuration, and a written report
// is a file. Passing them in keeps this package from depending on either.
func (s *Store) State(sessionID string, hasScope, hasReport bool) ([]PhaseState, error) {
	list, err := s.List(sessionID)
	if err != nil {
		return nil, err
	}

	// Count evidence per intel type.
	counts := map[IntelType]int{}
	for _, i := range list {
		counts[i.Type]++
	}
	if hasScope {
		counts[IntelScope]++
	}
	if hasReport {
		counts[IntelReport]++
	}

	states := make([]PhaseState, 0, len(Phases))
	completed := map[string]bool{}
	for _, p := range Phases {
		evidence := 0
		for _, t := range p.Produces {
			evidence += counts[t]
		}

		status := NotStarted
		if evidence > 0 {
			status = Complete
			// A phase with evidence but an unmet prerequisite is building on
			// sand — worth flagging.
			for _, req := range p.Requires {
				if !completed[req] {
					status = Blocked
					break
				}
			}
		} else {
			// Not yet started, unless a later phase already has evidence, in
			// which case this one was skipped — surface it as in-progress so it
			// is not mistaken for "not reached yet".
			for _, t := range p.Produces {
				_ = t
			}
		}
		if status == Complete || status == Blocked {
			completed[p.Name] = true
		}

		states = append(states, PhaseState{
			Phase: p, Name: p.Name, Title: p.Title, Summary: p.Summary,
			Status: status, Evidence: evidence, Roles: p.Roles, Requires: p.Requires,
		})
	}

	// Second pass: a not-started phase that precedes a phase with evidence was
	// skipped, not merely unreached.
	lastEvidence := -1
	for i, st := range states {
		if st.Evidence > 0 {
			lastEvidence = i
		}
	}
	for i := range states {
		if states[i].Status == NotStarted && i < lastEvidence {
			states[i].Status = InProgress
		}
	}
	return states, nil
}

// NextStep returns the phase the agent should focus on, and a directive.
func NextStep(states []PhaseState) (PhaseState, string) {
	for _, st := range states {
		switch st.Status {
		case NotStarted, InProgress:
			return st, fmt.Sprintf("Focus on **%s**: %s", st.Title, st.Summary)
		case Blocked:
			return st, fmt.Sprintf("**%s** has findings but %s was skipped — go back and cover it first.",
				st.Title, strings.Join(st.Requires, ", "))
		}
	}
	// Everything complete.
	return PhaseState{}, "Every phase has evidence. Review the coverage and compile the report."
}

func (s *Store) load(sessionID string) ([]Intel, error) {
	raw, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []Intel
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) save(sessionID string, list []Intel) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(sessionID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(sessionID))
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

// _ keeps phaseByName referenced for callers/tests even if unused here.
var _ = phaseByName
