package server

import (
	"errors"
	"net/http"
	"strings"
)

// handleAsks lists ask_user questions currently waiting, so a client that
// reconnects mid-pause (a reload) can render them.
func (s *Server) handleAsks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"asks": s.agent.PendingAsks()})
}

// handleResolveAsk delivers the person's answer to a paused ask_user call, which
// unblocks the tool and lets the same turn continue. An unknown id means the
// run already ended or was cancelled.
func (s *Server) handleResolveAsk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answer string `json:"answer"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Answer) == "" {
		writeError(w, http.StatusBadRequest, errors.New("an answer is required"))
		return
	}
	if !s.agent.ResolveAsk(r.PathValue("id"), body.Answer) {
		writeError(w, http.StatusNotFound, errors.New("that question is no longer waiting"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
