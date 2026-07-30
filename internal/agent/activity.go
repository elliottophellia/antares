package agent

import (
	"sort"
	"sync"
	"time"
)

// bgActivity tracks background actions per session that never appear in the
// transcript as model tool calls — RAG indexing, per-file reindex, auto-context
// retrieval, conversation indexing. The dashboard's tools panel shows these
// alongside the transcript-derived tool counts.
type bgActivity struct {
	mu   sync.Mutex
	byID map[string]map[string]*BgStat
}

// BgStat is one background action's usage in a session.
type BgStat struct {
	Name  string    `json:"name"`
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
}

func newBgActivity() *bgActivity {
	return &bgActivity{byID: map[string]map[string]*BgStat{}}
}

// record bumps a background action's count and last-used time for a session.
func (b *bgActivity) record(sessionID, name string) {
	if b == nil || sessionID == "" || name == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.byID[sessionID]
	if m == nil {
		m = map[string]*BgStat{}
		b.byID[sessionID] = m
	}
	s := m[name]
	if s == nil {
		s = &BgStat{Name: name}
		m[name] = s
	}
	s.Count++
	s.Last = time.Now()
}

// list returns a session's background stats, most-recent first.
func (b *bgActivity) list(sessionID string) []BgStat {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.byID[sessionID]
	out := make([]BgStat, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// BackgroundActivity exposes a session's background-tool usage for the API.
func (a *Agent) BackgroundActivity(sessionID string) []BgStat {
	return a.bgAct.list(sessionID)
}
