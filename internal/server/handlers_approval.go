package server

import (
	"errors"
	"net/http"
)

// handleApprovals lists tool calls waiting on a decision.
func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"approvals": s.agent.PendingApprovals()})
}

// handleResolveApproval answers one. An unknown id means it already timed out,
// which is worth saying rather than reporting success.
func (s *Server) handleResolveApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Allow bool `json:"allow"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.agent.ResolveApproval(r.PathValue("id"), body.Allow) {
		writeError(w, http.StatusNotFound, errors.New("that request is no longer waiting — it may have timed out"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
