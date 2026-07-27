package server

import (
	"errors"
	"net/http"
)

// handlePlugins lists what is loaded, including the ones that failed, so a
// plugin that is not working is visible rather than simply absent.
func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	mgr := s.agent.Plugins()
	if mgr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "plugins": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"plugins": mgr.List(),
		"dirs":    s.config().Plugins.Dirs,
	})
}

// handleTogglePlugin turns one on or off for this process. It is not written
// to the config: a plugin you want off permanently should be removed, and this
// is for trying one without a restart.
func (s *Server) handleTogglePlugin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	mgr := s.agent.Plugins()
	if mgr == nil {
		writeError(w, http.StatusBadRequest, errors.New("plugins are switched off"))
		return
	}
	if !mgr.SetEnabled(r.PathValue("name"), body.Enabled) {
		writeError(w, http.StatusNotFound, errors.New("no plugin by that name"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleReloadPlugins rescans the plugin directories.
func (s *Server) handleReloadPlugins(w http.ResponseWriter, r *http.Request) {
	mgr := s.agent.Plugins()
	if mgr == nil {
		writeError(w, http.StatusBadRequest, errors.New("plugins are switched off"))
		return
	}
	if err := mgr.Load(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plugins": mgr.List()})
}
