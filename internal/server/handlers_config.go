package server

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
)

func configPath() string { return config.ConfigFile() }

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"values":  s.config().Redacted(),
		"schema":  config.Schema(),
		"profile": config.ActiveProfile(),
		"path":    configPath(),
	})
}

func (s *Server) handleConfigSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"schema": config.Schema()})
}

// handleUpdateConfig applies dotted-path updates and reloads dependent services.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Updates map[string]any `json:"updates"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(body.Updates) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no changes supplied"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Apply in a deterministic order so an error message names the same field
	// on every retry.
	paths := make([]string, 0, len(body.Updates))
	for p := range body.Updates {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		value := body.Updates[path]
		// A redacted secret coming back unchanged means "leave it alone".
		if str, ok := value.(string); ok && strings.Contains(str, "••••") {
			continue
		}
		if err := cfg.SetPath(path, value); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": len(paths), "values": s.config().Redacted()})
}

func (s *Server) handleGetRawConfig(w http.ResponseWriter, r *http.Request) {
	text, err := config.Raw()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"yaml": text, "path": configPath()})
}

func (s *Server) handleSaveRawConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		YAML string `json:"yaml"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveRaw(body.YAML); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// applyReload rebuilds services that depend on configuration.
func (s *Server) applyReload() error {
	if s.reloadFn == nil {
		cfg := config.Get()
		s.SetConfig(cfg)
		s.agent.SetConfig(cfg)
		return nil
	}
	if err := s.reloadFn(); err != nil {
		return err
	}
	s.SetConfig(config.Get())
	// The agent owns the rebuilt skill library after a reload.
	if m := s.agent.Skills(); m != nil {
		s.skills = m
	}
	return nil
}

// ---- models -----------------------------------------------------------------

func (s *Server) handleModelOptions(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)

	type providerInfo struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Kind    string `json:"kind"`
		Enabled bool   `json:"enabled"`
		HasKey  bool   `json:"has_key"`
		BaseURL string `json:"base_url"`
		Active  bool   `json:"active"`
	}
	providers := make([]providerInfo, 0, len(names))
	for _, name := range names {
		p := cfg.Providers[name]
		providers = append(providers, providerInfo{
			ID: name, Label: firstNonEmpty(p.Label, name), Kind: p.Kind, Enabled: p.Enabled,
			HasKey: p.APIKey != "" || isLocalEndpoint(p.BaseURL), BaseURL: p.BaseURL,
			Active: name == cfg.Model.Provider,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active":    map[string]string{"model": cfg.Model.Default, "provider": cfg.Model.Provider},
		"providers": providers,
	})
}

func (s *Server) handleModelList(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")

	// Calling a provider we know has no credential just turns a known state
	// into an opaque 401. Report the missing key instead.
	id, p := s.config().ResolveProvider(provider)
	if p.APIKey == "" && !isLocalEndpoint(p.BaseURL) {
		writeJSON(w, http.StatusOK, map[string]any{
			"models": []any{}, "needs_key": true, "provider": id,
		})
		return
	}

	models, err := s.agent.Models(r.Context(), provider)
	if err != nil {
		// Report as a soft error so the page can still render the provider tab.
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) handleModelSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Model) == "" {
		writeError(w, http.StatusBadRequest, errors.New("model is required"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cfg.Model.Default = body.Model
	if body.Provider != "" {
		cfg.Model.Provider = body.Provider
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"model": body.Model, "provider": cfg.Model.Provider})
}

// ---- tools ------------------------------------------------------------------

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	active := map[string]bool{}
	for _, t := range s.agent.Registry().Resolve(cfg.Tools.Toolset, cfg.Tools.Enabled, cfg.Tools.Disabled) {
		active[t.Name()] = true
	}

	type toolInfo struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		Enabled          bool     `json:"enabled"`
		RequiresApproval bool     `json:"requires_approval"`
		Toolsets         []string `json:"toolsets"`
	}
	all := s.agent.Registry().All()
	out := make([]toolInfo, 0, len(all))
	for _, t := range all {
		sets := tools.ToolsetsFor(t.Name())
		sort.Strings(sets)
		out = append(out, toolInfo{
			Name: t.Name(), Description: t.Description(), Enabled: active[t.Name()],
			RequiresApproval: tools.NeedsApproval(t), Toolsets: sets,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"toolset":  cfg.Tools.Toolset,
		"toolsets": tools.ToolsetNames(),
		"tools":    out,
	})
}

func (s *Server) handleToggleTool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
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
	cfg.Tools.Enabled = removeString(cfg.Tools.Enabled, body.Name)
	cfg.Tools.Disabled = removeString(cfg.Tools.Disabled, body.Name)
	if body.Enabled {
		cfg.Tools.Enabled = append(cfg.Tools.Enabled, body.Name)
	} else {
		cfg.Tools.Disabled = append(cfg.Tools.Disabled, body.Name)
	}
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

func (s *Server) handleSetToolset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Toolset string `json:"toolset"`
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
	cfg.Tools.Toolset = body.Toolset
	// Switching preset clears per-tool overrides so the choice is predictable.
	cfg.Tools.Enabled = nil
	cfg.Tools.Disabled = nil
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

func removeString(list []string, want string) []string {
	out := list[:0]
	for _, v := range list {
		if v != want {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
