// Package roleperf tracks how each specialist role performs, so the system can
// tell which of them is delivering.
//
// A team that never notices which member does good work cannot improve. Every
// delegated task is a small trial: did the specialist finish cleanly, did it
// take forever, was its work kept or thrown away. Recording that gives a score
// that means something — not a vanity metric, but a signal the coordinator can
// weigh when it decides who to hand the next piece of work to.
package roleperf

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Stats are one role's running record.
type Stats struct {
	Role string `json:"role"`
	// Missions is how many tasks were delegated to this role.
	Missions int `json:"missions"`
	// Successes are the ones that finished cleanly.
	Successes int `json:"successes"`
	// Failures are the ones that errored or produced nothing.
	Failures int `json:"failures"`
	// Kept counts missions whose work was kept (a dirty worktree left for
	// review, a finding recorded) rather than discarded.
	Kept int `json:"kept"`
	// TotalTurns is the sum of turns across missions, for an efficiency measure.
	TotalTurns int `json:"total_turns"`
	// Score is the computed 0–100 rating, refreshed on every record.
	Score     float64   `json:"score"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Outcome is what a mission produced.
type Outcome struct {
	Role    string
	Success bool
	// Kept marks that the work was worth keeping.
	Kept  bool
	Turns int
}

// Tracker holds every role's stats, persisted to one JSON file.
type Tracker struct {
	path  string
	mu    sync.Mutex
	stats map[string]*Stats
}

// NewTracker loads or starts a tracker at path.
func NewTracker(path string) *Tracker {
	t := &Tracker{path: path, stats: map[string]*Stats{}}
	t.load()
	return t
}

// Record folds a mission outcome into a role's stats and returns the fresh
// score.
func (t *Tracker) Record(o Outcome) float64 {
	if o.Role == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.stats[o.Role]
	if s == nil {
		s = &Stats{Role: o.Role}
		t.stats[o.Role] = s
	}
	s.Missions++
	if o.Success {
		s.Successes++
	} else {
		s.Failures++
	}
	if o.Kept {
		s.Kept++
	}
	if o.Turns > 0 {
		s.TotalTurns += o.Turns
	}
	s.Score = score(s)
	s.UpdatedAt = time.Now()
	t.save()
	return s.Score
}

// Get returns one role's stats.
func (t *Tracker) Get(role string) (Stats, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.stats[role]
	if !ok {
		return Stats{}, false
	}
	return *s, true
}

// List returns every role's stats, best score first.
func (t *Tracker) List() []Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Stats, 0, len(t.stats))
	for _, s := range t.stats {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Missions > out[j].Missions
	})
	return out
}

// score rates a role 0–100 from its record. Three things matter, in order:
// how often it succeeds, how often its work is kept, and how few turns it
// takes. A role with no missions yet starts neutral rather than at zero — an
// untried specialist is not a bad one.
func score(s *Stats) float64 {
	if s.Missions == 0 {
		return 50
	}
	successRate := float64(s.Successes) / float64(s.Missions)
	keepRate := 0.0
	if s.Successes > 0 {
		keepRate = float64(s.Kept) / float64(s.Successes)
	}
	// Efficiency: fewer turns per mission is better, normalised so that ~4
	// turns is average and 20+ is poor.
	efficiency := 1.0
	if s.Missions > 0 && s.TotalTurns > 0 {
		avg := float64(s.TotalTurns) / float64(s.Missions)
		efficiency = math.Max(0, 1-(avg-4)/16)
		efficiency = math.Min(1, efficiency)
	}
	raw := successRate*0.55 + keepRate*0.25 + efficiency*0.20
	return math.Round(raw*1000) / 10 // one decimal place
}

func (t *Tracker) load() {
	if t.path == "" {
		return
	}
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var list []Stats
	if json.Unmarshal(raw, &list) != nil {
		return
	}
	for i := range list {
		s := list[i]
		t.stats[s.Role] = &s
	}
}

func (t *Tracker) save() {
	if t.path == "" {
		return
	}
	list := make([]Stats, 0, len(t.stats))
	for _, s := range t.stats {
		list = append(list, *s)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(t.path), 0o755)
	tmp := t.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		_ = os.Rename(tmp, t.path)
	}
}
