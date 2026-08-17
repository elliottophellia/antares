package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/engagement"
	"github.com/enowdev/antares/internal/store"
)

// handleEngagementReport renders a session's security findings (plus recorded
// intel) as a Markdown report and serves it as a file download.
func (s *Server) handleEngagementReport(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, errors.New("session is required"))
		return
	}
	if s.agent.Findings() == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("findings store is unavailable"))
		return
	}
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	if title == "" {
		title = "Security Assessment"
	}
	md, err := s.agent.Findings().Report(sessionID, title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Append recorded intelligence so the download is the full picture.
	if s.agent.Intel() != nil {
		if intel, e := s.agent.Intel().List(sessionID); e == nil && len(intel) > 0 {
			var b strings.Builder
			b.WriteString(md)
			b.WriteString("\n\n## Recorded intelligence\n\n")
			for _, it := range intel {
				if strings.TrimSpace(it.Detail) != "" {
					fmt.Fprintf(&b, "- **%s** — %s\n", it.Value, it.Detail)
				} else {
					fmt.Fprintf(&b, "- %s\n", it.Value)
				}
			}
			md = b.String()
		}
	}

	name := "antares-report-" + sanitizeName(sessionID) + ".md"
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write([]byte(md))
}

func sanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

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
	out := make([]row, 0, len(sessions))
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

// handleEngagementDelete clears a session's engagement state — its findings and
// recorded intel. With both gone the session drops out of the engagement list
// (which only shows sessions that have some). The chat session itself is left
// alone; delete that from the Sessions page.
func (s *Server) handleEngagementDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("session"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, errors.New("session is required"))
		return
	}
	if s.agent.Findings() != nil {
		if err := s.agent.Findings().Clear(sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	if s.agent.Intel() != nil {
		if err := s.agent.Intel().Clear(sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
