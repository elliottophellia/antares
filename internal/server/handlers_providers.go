package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/config"
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

// handleAddProviderModel adds a model id to providers.<id>.models, with an
// optional context window stored in model_meta. Manually added models then
// appear in the model list alongside auto-discovered ones (see agent.Models).
func (s *Server) handleAddProviderModel(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	var body struct {
		BaseURL     *string           `json:"base_url"`
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
	if body.BaseURL != nil {
		p.BaseURL = strings.TrimSpace(*body.BaseURL)
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
