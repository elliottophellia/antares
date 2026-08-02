package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/plugin"
	"gopkg.in/yaml.v3"
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

// handleAddPlugin writes a plugin.yaml from a dashboard form into the first
// plugin directory, then rescans so it is usable without a manual file drop.
// The executable itself is the user's to provide — we scaffold the manifest and
// point at where it goes.
func (s *Server) handleAddPlugin(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	mgr := s.agent.Plugins()
	if mgr == nil {
		writeError(w, http.StatusBadRequest, errors.New("plugins are switched off"))
		return
	}
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Command     string   `json:"command"`
		Args        []string `json:"args"`
		Hooks       []string `json:"hooks"`
		TimeoutMS   int      `json:"timeout_ms"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	// Keep the directory name a safe single path segment.
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		writeError(w, http.StatusBadRequest, errors.New("name must be a plain directory name"))
		return
	}
	if strings.TrimSpace(body.Command) == "" {
		writeError(w, http.StatusBadRequest, errors.New("command is required"))
		return
	}

	hooks := make([]plugin.Event, 0, len(body.Hooks))
	for _, h := range body.Hooks {
		e := plugin.Event(strings.TrimSpace(h))
		if !plugin.IsValidEvent(e) {
			writeError(w, http.StatusBadRequest, errors.New("unknown hook: "+h))
			return
		}
		hooks = append(hooks, e)
	}

	dir := s.pluginDir()
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": name + " already exists"})
		return
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Marshal a map, not the Manifest struct, so empty fields (version, env,
	// zero timeout) do not clutter the generated file.
	man := map[string]any{
		"name":    name,
		"command": strings.TrimSpace(body.Command),
		"hooks":   hooks,
	}
	if d := strings.TrimSpace(body.Description); d != "" {
		man["description"] = d
	}
	if a := trimList(body.Args); len(a) > 0 {
		man["args"] = a
	}
	if body.TimeoutMS > 0 {
		man["timeout_ms"] = body.TimeoutMS
	}
	out, err := yaml.Marshal(man)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dest, "plugin.yaml"), out, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := mgr.Load(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name, "dir": dest})
}

// pluginDir is where a new plugin lands: the first configured directory, or the
// one under the Antares home.
func (s *Server) pluginDir() string {
	if dirs := s.config().Plugins.Dirs; len(dirs) > 0 && strings.TrimSpace(dirs[0]) != "" {
		return config.Expand(dirs[0])
	}
	return config.Path("plugins")
}

// handleReloadPlugins rescans the plugin directories.
func (s *Server) handleReloadPlugins(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
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
