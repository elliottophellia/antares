package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
)

// Manager owns the configured MCP servers and exposes their tools to the agent.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client
	errs    map[string]string
}

// NewManager returns an empty manager.
func NewManager() *Manager {
	return &Manager{clients: map[string]*Client{}, errs: map[string]string{}}
}

// Connect brings up every enabled server, recording rather than propagating
// individual failures so one bad server cannot block startup.
func (m *Manager) Connect(ctx context.Context, cfg *config.Config) {
	if !cfg.MCP.Enabled {
		return
	}
	names := make([]string, 0, len(cfg.MCP.Servers))
	for name := range cfg.MCP.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var wg sync.WaitGroup
	for _, name := range names {
		sc := cfg.MCP.Servers[name]
		if !sc.Enabled {
			continue
		}
		wg.Add(1)
		go func(name string, sc config.MCPServer) {
			defer wg.Done()
			dialCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()

			client, err := Connect(dialCtx, name, ServerConfig{
				Transport: sc.Transport, Command: sc.Command, Args: sc.Args,
				Env: sc.Env, URL: sc.URL, Headers: sc.Headers,
			})
			m.mu.Lock()
			defer m.mu.Unlock()
			if err != nil {
				m.errs[name] = err.Error()
				slog.Warn("mcp server unavailable", "server", name, "error", err)
				return
			}
			delete(m.errs, name)
			m.clients[name] = client
		}(name, sc)
	}
	wg.Wait()
}

// Close shuts every server down.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, c := range m.clients {
		_ = c.Close()
		delete(m.clients, name)
	}
}

// ServerStatus reports one server for the dashboard.
type ServerStatus struct {
	Name      string    `json:"name"`
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	Tools     []ToolDef `json:"tools"`
}

// Status lists every configured server and its tools.
func (m *Manager) Status(cfg *config.Config) []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(cfg.MCP.Servers))
	for name := range cfg.MCP.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]ServerStatus, 0, len(names))
	for _, name := range names {
		st := ServerStatus{Name: name, Error: m.errs[name], Tools: []ToolDef{}}
		if c, ok := m.clients[name]; ok {
			st.Connected = true
			st.Tools = c.Tools()
		}
		out = append(out, st)
	}
	return out
}

// mcpToolName namespaces a remote tool so two servers cannot collide.
func mcpToolName(server, tool string) string {
	return "mcp__" + sanitize(server) + "__" + sanitize(tool)
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Register publishes every connected server's tools into the registry.
func (m *Manager) Register(reg *tools.Registry) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var registered []string
	for serverName, client := range m.clients {
		for _, def := range client.Tools() {
			t := &remoteTool{
				name:   mcpToolName(serverName, def.Name),
				server: serverName,
				remote: def.Name,
				desc:   def.Description,
				schema: def.InputSchema,
				client: client,
			}
			reg.Register(t)
			registered = append(registered, t.name)
		}
	}
	sort.Strings(registered)
	return registered
}

// remoteTool adapts one MCP tool to the local tool interface.
type remoteTool struct {
	name   string
	server string
	remote string
	desc   string
	schema map[string]any
	client *Client
}

func (t *remoteTool) Name() string { return t.name }

func (t *remoteTool) Description() string {
	if t.desc == "" {
		return fmt.Sprintf("Tool %q provided by the %q MCP server.", t.remote, t.server)
	}
	return t.desc + fmt.Sprintf(" (via the %q MCP server)", t.server)
}

func (t *remoteTool) Schema() map[string]any {
	if t.schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return t.schema
}

// RequiresApproval is conservative: a remote tool's side effects are unknown.
func (t *remoteTool) RequiresApproval() bool { return true }

func (t *remoteTool) Execute(ctx context.Context, in tools.Input) tools.Result {
	var args map[string]any
	if len(in.Args) > 0 {
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return tools.Errorf("invalid arguments: %v", err)
		}
	}

	in.Emit(tools.Progress{Tool: t.name, Message: "calling " + t.server})
	res, err := t.client.Call(ctx, t.remote, args)
	if err != nil {
		return tools.Errorf("MCP call failed: %v", err)
	}
	return tools.Result{
		Content: res.Text,
		IsError: res.IsError,
		Meta:    map[string]any{"server": t.server, "tool": t.remote},
	}
}
