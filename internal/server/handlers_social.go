package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/secret"
	"github.com/enowdev/antares/internal/socialimap"
	"github.com/enowdev/antares/internal/store"
)

// socialAccountView is the redacted API representation of a SocialAccount.
// Passwords and recovery codes are never included.
type socialAccountView struct {
	ID            string     `json:"id"`
	Platform      string     `json:"platform"`
	DisplayName   string     `json:"display_name"`
	Username      string     `json:"username"`
	ProfileURL    string     `json:"profile_url"`
	Status        string     `json:"status"`
	RAGNamespace  string     `json:"rag_namespace"`
	SkillName     string     `json:"skill_name"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	HasPassword   bool       `json:"has_password"`
	HasRecovery   bool       `json:"has_recovery"`
}

func socialView(a *store.SocialAccount) socialAccountView {
	return socialAccountView{
		ID:            a.ID,
		Platform:      a.Platform,
		DisplayName:   a.DisplayName,
		Username:      a.Username,
		ProfileURL:    a.ProfileURL,
		Status:        a.Status,
		RAGNamespace:  a.RAGNamespace,
		SkillName:     a.SkillName,
		LastCheckedAt: a.LastCheckedAt,
		CreatedAt:     a.CreatedAt,
		UpdatedAt:     a.UpdatedAt,
		HasPassword:   a.Password != "",
		HasRecovery:   a.RecoveryCodes != "",
	}
}

// handleSocialStatus returns the aggregate social media feature state.
func (s *Server) handleSocialStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	encryptionReady := secret.SocialAvailable()

	var browserState string = "disabled"
	var browserErr string
	if s.social != nil && cfg.Social.BrowserEnabled {
		st, errMsg := s.social.Status()
		browserState = string(st)
		browserErr = errMsg
	}

	// IMAP is configured if username is present; password is not checked here.
	imapConfigured := cfg.Social.IMAPUsername != ""

	var accounts []socialAccountView
	if encryptionReady && s.db != nil {
		if list, err := s.db.ListSocialAccounts(r.Context()); err == nil {
			for i := range list {
				accounts = append(accounts, socialView(&list[i]))
			}
		}
	}
	if accounts == nil {
		accounts = []socialAccountView{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":           cfg.Social.Enabled,
		"encryption_ready":  encryptionReady,
		"imap_configured":   imapConfigured,
		"imap_host":         cfg.Social.IMAPHost,
		"imap_port":         cfg.Social.IMAPPort,
		"imap_username":     cfg.Social.IMAPUsername,
		"browser": map[string]any{
			"enabled":  cfg.Social.BrowserEnabled,
			"state":    browserState,
			"error":    browserErr,
		},
		"autopilot_enabled": cfg.Social.AutopilotEnabled,
		"accounts":          accounts,
	})
}

// handleSocialIMAPTest validates an IMAP connection without persisting.
func (s *Server) handleSocialIMAPTest(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := socialimap.Config{
		Host:     strings.TrimSpace(body.Host),
		Port:     body.Port,
		Username: strings.TrimSpace(body.Username),
		Password: body.Password,
	}
	if cfg.Host == "" {
		cfg.Host = "imap.gmail.com"
	}
	if cfg.Port == 0 {
		cfg.Port = 993
	}
	if cfg.Username == "" || cfg.Password == "" {
		writeError(w, http.StatusBadRequest, errors.New("username and password are required"))
		return
	}

	count, err := cfg.Test()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "inbox_count": count})
}

// handleSocialIMAPSave persists IMAP settings. The password is encrypted
// via the social master key and stored in KV.
func (s *Server) handleSocialIMAPSave(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if strings.TrimSpace(body.Username) == "" {
		writeError(w, http.StatusBadRequest, errors.New("username is required"))
		return
	}

	// Encrypt the password via social master key.
	key, err := secret.SocialDefault()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("social encryption is not set up — generate a master key first"))
		return
	}
	box, err := key.Box()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	encPass, err := box.Encrypt(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Store non-secret settings in config and secret in KV.
	cfg := s.config()
	host := strings.TrimSpace(body.Host)
	if host == "" {
		host = "imap.gmail.com"
	}
	port := body.Port
	if port == 0 {
		port = 993
	}
	_ = cfg.SetPath("social.enabled", true)
	_ = cfg.SetPath("social.imap_host", host)
	_ = cfg.SetPath("social.imap_port", port)
	_ = cfg.SetPath("social.imap_username", body.Username)

	// KV stores the encrypted password.
	ctx := r.Context()
	_ = s.db.SetKV(ctx, "social:imap_password", encPass)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSocialBrowserStart launches the persistent social browser.
func (s *Server) handleSocialBrowserStart(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	if s.social == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("social browser manager is not available"))
		return
	}

	go s.social.Start(r.Context())

	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "browser starting"})
}

// handleSocialBrowserStop closes the persistent social browser.
func (s *Server) handleSocialBrowserStop(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	if s.social == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("social browser manager is not available"))
		return
	}
	s.social.Stop()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSocialBrowserOpen opens or focuses the browser window.
func (s *Server) handleSocialBrowserOpen(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	if s.social == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("social browser manager is not available"))
		return
	}
	state, _ := s.social.Status()
	if state != "running" {
		writeError(w, http.StatusConflict, errors.New("browser is not running"))
		return
	}
	// The browser is already visible (headless=false); this is a no-op
	// placeholder for future window-focus CDP commands.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSocialAutopilot toggles the social media autopilot.
func (s *Server) handleSocialAutopilot(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.config()
	_ = cfg.SetPath("social.autopilot_enabled", body.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Enabled})
}

// handleSocialEncryptionSetup generates a new master key and returns the
// one-time recovery key.
func (s *Server) handleSocialEncryptionSetup(w http.ResponseWriter, r *http.Request) {
	// This is a bootstrap operation: loopback or bearer only.
	if !requestIsLoopback(r) && !s.bearerAuthorized(r) {
		writeError(w, http.StatusForbidden, errors.New("encryption setup is available only from loopback or with a configured bearer token"))
		return
	}

	key, err := secret.SocialGenerateKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	recoveryKey := base64.StdEncoding.EncodeToString(key)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"recovery_key": recoveryKey,
		"message":      "Save this recovery key. It will never be shown again. If you lose both the key file and this recovery key, all social media credentials will be unrecoverable.",
	})
}

// handleSocialListAccounts returns all social accounts (redacted).
func (s *Server) handleSocialListAccounts(w http.ResponseWriter, r *http.Request) {
	if !secret.SocialAvailable() {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	list, err := s.db.ListSocialAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]socialAccountView, 0, len(list))
	for i := range list {
		views = append(views, socialView(&list[i]))
	}
	writeJSON(w, http.StatusOK, views)
}

// handleSocialAddAccount creates a new social account with encrypted credentials.
func (s *Server) handleSocialAddAccount(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	if !secret.SocialAvailable() {
		writeError(w, http.StatusServiceUnavailable, errors.New("social encryption is not set up — generate a master key first"))
		return
	}
	var body struct {
		Platform      string `json:"platform"`
		DisplayName   string `json:"display_name"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		RecoveryCodes string `json:"recovery_codes"`
		ProfileURL    string `json:"profile_url"`
		Status        string `json:"status"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Platform) == "" || strings.TrimSpace(body.Username) == "" {
		writeError(w, http.StatusBadRequest, errors.New("platform and username are required"))
		return
	}
	if body.Status == "" {
		body.Status = "not_created"
	}

	acct := &store.SocialAccount{
		ID:            newID("soc"),
		Platform:      body.Platform,
		DisplayName:   body.DisplayName,
		Username:      body.Username,
		Password:      body.Password,
		RecoveryCodes: body.RecoveryCodes,
		ProfileURL:    body.ProfileURL,
		Status:        body.Status,
		RAGNamespace:  "social/" + body.Platform,
		SkillName:     "social-" + body.Platform,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := s.db.PutSocialAccount(r.Context(), acct); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, socialView(acct))
}

// handleSocialDeleteAccount removes a social account.
func (s *Server) handleSocialDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("account id is required"))
		return
	}
	if err := s.db.DeleteSocialAccount(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
