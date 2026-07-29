package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// dashCookie is the name of the dashboard login session cookie.
const dashCookie = "antares_dash"

// dashSessionTTL is how long a dashboard login stays valid.
const dashSessionTTL = 30 * 24 * time.Hour

// withDashboardAuth gates the web dashboard behind the login password when one
// is configured. It is web-only: it never applies to the TUI or gateways
// (those talk to the agent in-process, not over HTTP), and any client that
// presents the configured server.auth_token as a bearer bypasses it — so the
// CLI and scripted API callers keep working. Requests without a valid session
// cookie get 401 on /api/* (except the auth and health endpoints the login
// page itself needs), which the dashboard turns into a redirect to /login.
func (s *Server) withDashboardAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		if !cfg.Server.DashboardLocked() {
			next.ServeHTTP(w, r)
			return
		}
		// Only guard the API surface; the SPA shell and static assets load so
		// the login page can render.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Endpoints the login flow and liveness checks must reach unauthenticated.
		switch r.URL.Path {
		case "/api/health", "/api/auth/login", "/api/auth/logout", "/api/auth/status":
			next.ServeHTTP(w, r)
			return
		}
		// A valid bearer auth_token bypasses the dashboard login (CLI, scripts).
		if tok := strings.TrimSpace(cfg.Server.AuthToken); tok != "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") &&
				strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")) == tok {
				next.ServeHTTP(w, r)
				return
			}
		}
		if s.dashSessionValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, errors.New("dashboard login required"))
	})
}

// dashSessionValid reports whether the request carries a live dashboard session
// cookie, pruning it if expired.
func (s *Server) dashSessionValid(r *http.Request) bool {
	c, err := r.Cookie(dashCookie)
	if err != nil || c.Value == "" {
		return false
	}
	s.dashMu.Lock()
	defer s.dashMu.Unlock()
	exp, ok := s.dashSessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.dashSessions, c.Value)
		return false
	}
	return true
}

// handleAuthStatus tells the dashboard whether a login is required and whether
// the current request is already authenticated.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	locked := cfg.Server.DashboardLocked()
	writeJSON(w, http.StatusOK, map[string]any{
		"password_required": locked,
		"authenticated":     !locked || s.dashSessionValid(r),
	})
}

// handleAuthLogin verifies the dashboard password and, on success, mints a
// session cookie.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.config()
	if !cfg.Server.DashboardLocked() {
		// Nothing to log into; report success so the UI proceeds.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if !config.CheckPassword(cfg.Server.DashboardPasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, errors.New("incorrect password"))
		return
	}

	tok := newSessionToken()
	s.dashMu.Lock()
	s.dashSessions[tok] = time.Now().Add(dashSessionTTL)
	s.dashMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     dashCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(dashSessionTTL / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthLogout drops the caller's dashboard session.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(dashCookie); err == nil && c.Value != "" {
		s.dashMu.Lock()
		delete(s.dashSessions, c.Value)
		s.dashMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     dashCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// invalidateDashSessions drops every active dashboard session. Call after the
// password changes so old logins cannot outlive it.
func (s *Server) invalidateDashSessions() {
	s.dashMu.Lock()
	s.dashSessions = map[string]time.Time{}
	s.dashMu.Unlock()
}

func newSessionToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
