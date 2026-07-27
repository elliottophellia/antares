package server

import "net/http"

// handleRoles lists the specialist roles for the dashboard.
func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	reg := s.agent.Roles()
	if reg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"roles": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"roles": reg.List(),
		"scope": s.config().Security.Scope,
	})
}
