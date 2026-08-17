package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/providers"
)

// handleProviderModelInfo tries to discover a model's context window from the
// provider's live model list, so the UI can auto-fill it when adding a model.
// Returns { found, context_window }. A model the provider does not report is
// found:false — the caller then asks the user for the value.
func (s *Server) handleProviderModelInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	modelID := strings.TrimSpace(r.URL.Query().Get("id"))
	if modelID == "" {
		writeError(w, http.StatusBadRequest, errors.New("a model id is required"))
		return
	}
	models, err := s.agent.Models(r.Context(), id)
	if err != nil {
		// Fetch failed — not fatal, the UI falls back to manual entry.
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	for _, m := range models {
		if m.ID == modelID {
			writeJSON(w, http.StatusOK, map[string]any{
				"found":          true,
				"context_window": m.ContextWindow,
				"name":           m.Name,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": false})
}

// handleContextWindow reports the active model's token budget, so the composer's
// context gauge can show "0 / <window>" before the first turn (usage events
// carry the window once a turn runs, but not before one has). Mirrors the
// agent's own resolution: per-model provider meta, then the configured window,
// then a sane default.
func (s *Server) handleContextWindow(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	model := cfg.Model.Default
	// Same precedence as the agent's contextWindowFor: per-model meta override,
	// then the provider catalogue (real windows for known models like glm-5.2's
	// 1M), then the configured window, then a sane default.
	window := 128000
	if w := providers.ContextWindow(model); w > 0 {
		window = w
	} else if cfg.Model.ContextWindow > 0 {
		window = cfg.Model.ContextWindow
	}
	for _, p := range cfg.Providers {
		if m, ok := p.ModelMeta[model]; ok && m.ContextWindow > 0 {
			window = m.ContextWindow
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"context_window": window, "model": model})
}

// handleAddProviderModel adds a model id to providers.<id>.models, with an
// optional context window stored in model_meta. Manually added models then
// appear in the model list alongside auto-discovered ones (see agent.Models).
func (s *Server) handleAddProviderModel(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Model         string `json:"model"`
		ContextWindow int    `json:"context_window"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	modelID := strings.TrimSpace(body.Model)
	if modelID == "" {
		writeError(w, http.StatusBadRequest, errors.New("a model id is required"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.Provider{}
	}
	p := cfg.Providers[id]
	// Append unless already present.
	exists := false
	for _, m := range p.Models {
		if m == modelID {
			exists = true
			break
		}
	}
	if !exists {
		p.Models = append(p.Models, modelID)
	}
	if body.ContextWindow > 0 {
		if p.ModelMeta == nil {
			p.ModelMeta = map[string]config.ModelMeta{}
		}
		p.ModelMeta[modelID] = config.ModelMeta{ContextWindow: body.ContextWindow}
	}
	cfg.Providers[id] = p

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDeleteProviderModel removes a manually added model id (and its meta).
func (s *Server) handleDeleteProviderModel(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	modelID := r.PathValue("model")

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	p := cfg.Providers[id]
	out := p.Models[:0]
	for _, m := range p.Models {
		if m != modelID {
			out = append(out, m)
		}
	}
	p.Models = out
	delete(p.ModelMeta, modelID)
	cfg.Providers[id] = p

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleProviderSettings saves per-provider settings: base URL, request
// timeout, and custom headers. Credentials go through the key endpoint; this is
// everything else a provider entry carries.
func (s *Server) handleProviderSettings(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	var body struct {
		BaseURL     *string           `json:"base_url"`
		Label       *string           `json:"label"`
		TimeoutSecs *int              `json:"timeout_seconds"`
		Headers     map[string]string `json:"headers"`
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
	p := cfg.Providers[id]
	// Custom providers (user-named entries, plus the legacy "custom" slot) may
	// point at loopback or LAN addresses; built-ins keep their catalogue rule.
	sp := lookupSetupProvider(cfg, id)
	custom := sp == nil || sp.Custom
	local := sp != nil && sp.Local
	if body.BaseURL != nil {
		baseURL := strings.TrimSpace(*body.BaseURL)
		if baseURL != "" {
			if err := s.validateChosenBaseURL(r.Context(), baseURL, custom, local); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		p.BaseURL = baseURL
	}
	if body.Label != nil {
		if label := strings.TrimSpace(*body.Label); label != "" {
			p.Label = label
		}
	}
	if body.TimeoutSecs != nil {
		p.TimeoutSecs = *body.TimeoutSecs
	}
	if body.Headers != nil {
		p.Headers = body.Headers
	}
	cfg.Providers[id] = p

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleCreateProvider adds a user-defined provider: a name, an
// OpenAI-compatible base URL, and an optional key. Any number may exist, and
// loopback/LAN endpoints are accepted — the user is pointing Antares at their
// own service.
func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("a name is required"))
		return
	}
	baseURL := strings.TrimSpace(body.BaseURL)
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, errors.New("a base URL is required"))
		return
	}
	if err := s.validateCustomProviderBaseURL(r.Context(), baseURL); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id := CustomProviderID(cfg, name)

	// Verify the pair now so a bad endpoint or key surfaces at creation time
	// rather than on the first turn. A keyless service is allowed.
	key := strings.TrimSpace(body.APIKey)
	if key != "" {
		client, err := llm.New(llm.Options{
			Kind: "openai-compatible", BaseURL: baseURL, APIKey: key,
			ProviderID: id, Timeout: 30 * time.Second,
		})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if _, err := client.Models(ctx); err != nil {
			if llm.IsAuthError(err) {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			if !llm.IsUnsupported(err) {
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"ok": false, "error": "The provider could not be reached or returned an invalid response: " + err.Error(),
				})
				return
			}
		}
	}

	cfg.Providers[id] = config.Provider{
		Kind: "openai-compatible", BaseURL: baseURL, APIKey: key,
		Enabled: true, Label: name,
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// handleDeleteProvider removes a user-defined provider. Built-in catalogue
// entries and the active provider are refused.
func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	// The legacy "custom" slot behaves like any user-defined provider: it can
	// be deleted. Other built-ins cannot.
	if isCatalogueProviderID(id) && id != "custom" {
		writeError(w, http.StatusBadRequest, errors.New("built-in providers cannot be deleted"))
		return
	}
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, ok := cfg.Providers[id]; !ok {
		writeError(w, http.StatusNotFound, errors.New("unknown provider"))
		return
	}
	if cfg.Model.Provider == id {
		writeError(w, http.StatusBadRequest, errors.New("this provider is active — pick another model before deleting it"))
		return
	}
	delete(cfg.Providers, id)
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
