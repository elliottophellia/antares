package server

import (
	"context"
	"encoding/json"
	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/mcp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeMCPRefresher struct {
	called bool
}

func (f *fakeMCPRefresher) Refresh(context.Context, *config.Config) []mcp.ServerStatus {
	f.called = true
	return []mcp.ServerStatus{{
		Name:      "ida",
		Started:   true,
		Connected: true,
		Tools:     []mcp.ToolDef{{Name: "server_health"}},
	}}
}

type fakeMCPManager struct {
	refreshed bool
	cfg       *config.Config
}

func (f *fakeMCPManager) Refresh(_ context.Context, cfg *config.Config) []mcp.ServerStatus {
	f.refreshed = true
	f.cfg = cfg
	return nil
}

func (f *fakeMCPManager) Status(*config.Config) []mcp.ServerStatus { return nil }

func newMCPMutationServer(t *testing.T) (*Server, *fakeMCPManager) {
	t.Helper()
	t.Setenv("ANTARES_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Server.DashboardPasswordHash = "test-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	manager := &fakeMCPManager{}
	return &Server{cfg: cfg, agent: &agent.Agent{}, mcpRefresh: manager, reloadFn: func() error { return nil }}, manager
}

func TestAddMCPServerRefreshesManager(t *testing.T) {
	s, manager := newMCPMutationServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/servers", strings.NewReader(`{"name":"manual","transport":"http","url":"http://127.0.0.1:8080/mcp"}`))
	w := httptest.NewRecorder()

	s.handleAddMCPServer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !manager.refreshed {
		t.Fatal("adding a server did not refresh MCP connections")
	}
	if _, ok := manager.cfg.MCP.Servers["manual"]; !ok {
		t.Fatalf("refresh config = %+v, want manually added server", manager.cfg.MCP.Servers)
	}
}

func TestHubInstallMCPRefreshesManager(t *testing.T) {
	s, manager := newMCPMutationServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/hub/mcp/install", strings.NewReader(`{"id":"filesystem"}`))
	w := httptest.NewRecorder()

	s.handleHubInstallMCP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !manager.refreshed {
		t.Fatal("installing a hub server did not refresh MCP connections")
	}
	if _, ok := manager.cfg.MCP.Servers["filesystem"]; !ok {
		t.Fatalf("refresh config = %+v, want installed hub server", manager.cfg.MCP.Servers)
	}
}

func TestDeleteMCPServerRefreshesManager(t *testing.T) {
	s, manager := newMCPMutationServer(t)
	cfg := s.cfg
	cfg.MCP.Enabled = true
	cfg.MCP.Servers = map[string]config.MCPServer{
		"remove-me": {Transport: "http", URL: "http://127.0.0.1:8080/mcp", Enabled: true},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("seed configured server: %v", err)
	}
	s.cfg = cfg
	req := httptest.NewRequest(http.MethodDelete, "/api/mcp/servers/remove-me", nil)
	req.SetPathValue("name", "remove-me")
	w := httptest.NewRecorder()

	s.handleDeleteMCPServer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !manager.refreshed {
		t.Fatal("deleting a server did not refresh MCP connections")
	}
	if _, ok := manager.cfg.MCP.Servers["remove-me"]; ok {
		t.Fatal("refresh config still contains deleted server")
	}
}

func TestMCPRefreshHandlerReturnsFreshStatus(t *testing.T) {
	s := &Server{cfg: &config.Config{MCP: config.MCP{Enabled: true}}}
	refresher := &fakeMCPRefresher{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/refresh", nil)
	w := httptest.NewRecorder()

	s.refreshMCP(w, req, refresher)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !refresher.called {
		t.Fatal("handler did not invoke MCP refresh")
	}
	var body struct {
		Enabled bool               `json:"enabled"`
		Servers []mcp.ServerStatus `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Enabled || len(body.Servers) != 1 || !body.Servers[0].Connected || len(body.Servers[0].Tools) != 1 {
		t.Fatalf("response = %+v, want refreshed connected server", body)
	}
}

func TestMCPRefreshHandlerRejectsDisabledMCP(t *testing.T) {
	s := &Server{cfg: &config.Config{MCP: config.MCP{Enabled: false}}}
	refresher := &fakeMCPRefresher{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/refresh", nil)
	w := httptest.NewRecorder()

	s.refreshMCP(w, req, refresher)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if refresher.called {
		t.Fatal("disabled MCP unexpectedly refreshed")
	}
}
