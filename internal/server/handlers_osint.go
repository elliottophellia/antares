package server

import (
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
)

// handleGoogleVerify checks whether the configured (or a supplied) Google
// cookie is a live session and reports every account it is signed into, so the
// Settings page can show them and let the user pick which one lookups use.
func (s *Server) handleGoogleVerify(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Cookie string `json:"cookie"`
	}
	_ = decodeBody(r, &body)
	raw := strings.TrimSpace(body.Cookie)
	if raw == "" {
		raw = strings.TrimSpace(s.config().OSINT.GoogleCookie)
	}
	if raw == "" {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "error": "no cookie configured"})
		return
	}
	accounts, err := tools.VerifyGoogleCookie(r.Context(), raw)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected": true, "accounts": accounts, "selected": s.config().OSINT.GoogleAuthUser,
	})
}

// handleGoogleSelect persists which account (the /u/<N>/ index) the Google
// lookups act as, chosen from the verified list.
func (s *Server) handleGoogleSelect(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		AuthUser int `json:"authuser"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cfg.OSINT.GoogleAuthUser = body.AuthUser
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authuser": body.AuthUser})
}
