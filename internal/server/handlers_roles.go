package server

import "net/http"

// handleRoles lists the specialist roles for the dashboard.
func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	reg := s.agent.Roles()
	if reg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"roles": []any{}})
		return
	}
	out := map[string]any{
		"roles":  reg.List(),
		"scope":  s.config().Security.Scope,
		"active": s.agent.ActiveAgents(),
	}
	if perf := s.agent.RolePerformance(); perf != nil {
		out["performance"] = perf.List()
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSwarm reports the sub-agents running right now, for a live panel.
func (s *Server) handleSwarm(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"active": s.agent.ActiveAgents()})
}
