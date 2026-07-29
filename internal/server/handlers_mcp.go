package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// handleAddMCPServer registers an MCP server typed in the dashboard and writes
// it to the config, so it survives a restart the same way a hub install does.
// This is the manual counterpart to /api/hub/mcp/install: for a server that is
// not in the catalogue — an internal endpoint, a command of your own.
func (s *Server) handleAddMCPServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string            `json:"name"`
		Transport string            `json:"transport"` // stdio|http
		Command   string            `json:"command"`
		Args      []string          `json:"args"`
		Env       map[string]string `json:"env"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
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
	transport := strings.TrimSpace(body.Transport)
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio":
		if strings.TrimSpace(body.Command) == "" {
			writeError(w, http.StatusBadRequest, errors.New("a stdio server needs a command"))
			return
		}
	case "http":
		if strings.TrimSpace(body.URL) == "" {
			writeError(w, http.StatusBadRequest, errors.New("an http server needs a url"))
			return
		}
	default:
		writeError(w, http.StatusBadRequest, errors.New("transport must be stdio or http"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if cfg.MCP.Servers == nil {
		cfg.MCP.Servers = map[string]config.MCPServer{}
	}
	if _, exists := cfg.MCP.Servers[name]; exists {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": name + " is already configured"})
		return
	}

	server := config.MCPServer{Enabled: true, Transport: transport}
	if transport == "stdio" {
		server.Command = strings.TrimSpace(body.Command)
		server.Args = trimList(body.Args)
		server.Env = nonEmptyMap(body.Env)
	} else {
		server.URL = strings.TrimSpace(body.URL)
		server.Headers = nonEmptyMap(body.Headers)
	}

	cfg.MCP.Servers[name] = server
	cfg.MCP.Enabled = true
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if s.mcp != nil {
		// Connecting can take seconds while npx downloads a package; do it off
		// the request so the form returns promptly.
		go s.mcp.Connect(context.Background(), s.config())
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

// handleDeleteMCPServer removes a server from the config and disconnects it.
func (s *Server) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, exists := cfg.MCP.Servers[name]; !exists {
		writeError(w, http.StatusNotFound, errors.New("no server by that name"))
		return
	}
	delete(cfg.MCP.Servers, name)
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if s.mcp != nil {
		go s.mcp.Connect(context.Background(), s.config())
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// trimList drops blank entries from a string slice supplied by a form.
func trimList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nonEmptyMap drops blank keys/values so an empty form field is not persisted.
func nonEmptyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		if k = strings.TrimSpace(k); k != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
