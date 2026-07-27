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
	// One manager-level tool surfaces resources across every server. The
	// mcp__ prefix means the toolset resolver includes it opt-out, like the
	// remote tools.
	if len(m.clients) > 0 {
		reg.Register(&resourceTool{m: m})
		registered = append(registered, "mcp__resource")
	}
	sort.Strings(registered)
	return registered
}

// resourceTool exposes MCP resources (list/read) across all connected servers.
type resourceTool struct{ m *Manager }

func (t *resourceTool) Name() string { return "mcp__resource" }
func (t *resourceTool) Description() string {
	return "List and read resources exposed by connected MCP servers. " +
		"`list` shows every available resource and its uri; `read` fetches one by uri."
}
func (t *resourceTool) RequiresApproval() bool { return false }
func (t *resourceTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"list", "read"}, "description": "What to do."},
			"uri":    map[string]any{"type": "string", "description": "For read: the resource uri from list."},
		},
		"required": []string{"action"},
	}
}

func (t *resourceTool) Execute(ctx context.Context, in tools.Input) tools.Result {
	var args struct {
		Action string `json:"action"`
		URI    string `json:"uri"`
	}
	if len(in.Args) > 0 {
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return tools.Errorf("invalid arguments: %v", err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list", "":
		res := t.m.ListResources(ctx)
		if len(res) == 0 {
			return tools.Text("No MCP resources are available.")
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d MCP resource(s):\n\n", len(res))
		for _, r := range res {
			name := r.Name
			if name == "" {
				name = r.URI
			}
			fmt.Fprintf(&b, "- %s (%s): %s\n", name, r.URI, r.Description)
		}
		b.WriteString("\nRead one with action=read and its uri.")
		return tools.Text(b.String())
	case "read":
		if strings.TrimSpace(args.URI) == "" {
			return tools.Errorf("uri is required to read a resource")
		}
		text, err := t.m.ReadResource(ctx, args.URI)
		if err != nil {
			return tools.Errorf("%v", err)
		}
		return tools.Text(text)
	default:
		return tools.Errorf("unknown action %q (want list or read)", args.Action)
	}
}

// ListResources aggregates resources from every connected server, prefixing
// each with its server so the same-named resource on two servers stays distinct.
func (m *Manager) ListResources(ctx context.Context) []ResourceView {
	m.mu.RLock()
	clients := make(map[string]*Client, len(m.clients))
	for k, v := range m.clients {
		clients[k] = v
	}
	m.mu.RUnlock()

	var out []ResourceView
	for server, client := range clients {
		res, err := client.ListResources(ctx)
		if err != nil {
			continue // server does not implement resources
		}
		for _, r := range res {
			out = append(out, ResourceView{
				Server: server, URI: r.URI, Name: r.Name, Description: r.Description, MimeType: r.MimeType,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URI < out[j].URI })
	return out
}

// ReadResource reads a resource by URI from whichever server exposes it.
func (m *Manager) ReadResource(ctx context.Context, uri string) (string, error) {
	m.mu.RLock()
	clients := make([]*Client, 0, len(m.clients))
	for _, v := range m.clients {
		clients = append(clients, v)
	}
	m.mu.RUnlock()

	var lastErr error
	for _, client := range clients {
		text, err := client.ReadResource(ctx, uri)
		if err == nil {
			return text, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no MCP server has a resource with uri %q", uri)
}

// ResourceView is one resource with the server that hosts it.
type ResourceView struct {
	Server      string
	URI         string
	Name        string
	Description string
	MimeType    string
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
