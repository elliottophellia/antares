package server

import (
	"net/http"

	"github.com/enowdev/antares/internal/engagement"
	"github.com/enowdev/antares/internal/store"
)

// handleEngagementSessions lists the sessions that have security-engagement data
// (findings or intel), for the page's session picker.
func (s *Server) handleEngagementSessions(w http.ResponseWriter, r *http.Request) {
	sessions, _, err := s.db.ListSessions(r.Context(), store.SessionFilter{Limit: 200})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Findings int    `json:"findings"`
		Intel    int    `json:"intel"`
	}
	out := make([]row, 0)
	for _, sess := range sessions {
		f, intel := 0, 0
		if s.agent.Findings() != nil {
			if list, err := s.agent.Findings().List(sess.ID); err == nil {
				f = len(list)
			}
		}
		if s.agent.Intel() != nil {
			if list, err := s.agent.Intel().List(sess.ID); err == nil {
				intel = len(list)
			}
		}
		if f > 0 || intel > 0 {
			out = append(out, row{ID: sess.ID, Title: sess.Title, Findings: f, Intel: intel})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleEngagement aggregates the methodology, coverage, chains, findings, and
// intel for one session.
func (s *Server) handleEngagement(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	cfg := s.config()
	hasScope := len(cfg.Security.Scope) > 0

	var findingsList any
	var evidence []string
	hasReport := false
	if s.agent.Findings() != nil {
		if list, err := s.agent.Findings().List(sessionID); err == nil {
			findingsList = list
			hasReport = len(list) > 0
			for _, f := range list {
				evidence = append(evidence, f.Title, f.CWE, f.Description)
			}
		}
	}

	var intelList any
	if s.agent.Intel() != nil {
		if list, err := s.agent.Intel().List(sessionID); err == nil {
			intelList = list
			for _, it := range list {
				evidence = append(evidence, it.Value, it.Detail)
			}
		}
	}

	var phases []engagement.PhaseState
	if s.agent.Intel() != nil {
		phases, _ = s.agent.Intel().State(sessionID, hasScope, hasReport)
	}

	cov := engagement.Coverage(evidence)
	type area struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Covered bool   `json:"covered"`
	}
	coverage := make([]area, 0, len(cov))
	for _, c := range cov {
		coverage = append(coverage, area{Name: c.Area.Name, Title: c.Area.Title, Covered: c.Covered})
	}
	_, nextStep := engagement.NextStep(phases)

	writeJSON(w, http.StatusOK, map[string]any{
		"phases":           phases,
		"coverage":         coverage,
		"coverage_percent": engagement.CoveragePercent(cov),
		"chains":           engagement.DetectChains(evidence),
		"findings":         findingsList,
		"intel":            intelList,
		"next":             nextStep,
		"scope":            cfg.Security.Scope,
	})
}
