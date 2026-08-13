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

	"github.com/enowdev/antares/internal/hub"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
)

// Manager owns the configured MCP servers and exposes their tools to the agent.
type Manager struct {
	mu         sync.RWMutex
	refreshMu  sync.Mutex
	clients    map[string]*Client
	errs       map[string]string
	registry   *tools.Registry
	registered []string
}

// NewManager returns an empty manager.
func NewManager() *Manager {
	return &Manager{clients: map[string]*Client{}, errs: map[string]string{}}
}

// Connect brings up every enabled server, recording rather than propagating
// individual failures so one bad server cannot block startup.
func (m *Manager) Connect(ctx context.Context, cfg *config.Config) {
	clients, errs := connectAll(ctx, cfg)
	m.mu.Lock()
	m.clients = clients
	m.errs = errs
	m.mu.Unlock()
}

func connectAll(ctx context.Context, cfg *config.Config) (map[string]*Client, map[string]string) {
	clients := map[string]*Client{}
	errs := map[string]string{}
	if !cfg.MCP.Enabled {
		return clients, errs
	}
	names := make([]string, 0, len(cfg.MCP.Servers))
	for name := range cfg.MCP.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	upgradeBuiltinServers(cfg)

	var wg sync.WaitGroup
	var mu sync.Mutex
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
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[name] = err.Error()
				slog.Warn("mcp: cannot connect", "server", name, "error", err)
				return
			}
			clients[name] = client
		}(name, sc)
	}
	wg.Wait()
	return clients, errs
}

// upgradeBuiltinServers repoints configurations still carrying the old
// unpinned catalogue defaults at the pinned ones. Only the package reference is
// rewritten — arguments the user edited (the directory the filesystem server
// exposes, --repository, a connection string) are carried over untouched, and a
// custom command or URL is left alone entirely.
func upgradeBuiltinServers(cfg *config.Config) {
	for name, server := range cfg.MCP.Servers {
		entry, ok := hub.LookupMCP(name)
		if !ok {
			continue
		}
		if entry.URL != "" {
			if !isLegacyEndpoint(server, entry) {
				continue
			}
			server.Transport = "http"
			server.URL = entry.URL
			cfg.MCP.Servers[name] = server
			slog.Info("mcp: migrated built-in server to its current endpoint", "server", name, "url", entry.URL)
			continue
		}
		args, upgraded := pinnedArgs(server, entry)
		if !upgraded {
			continue
		}
		server.Args = args
		cfg.MCP.Servers[name] = server
		slog.Info("mcp: pinned built-in server package", "server", name, "args", args)
	}
}

// isLegacyEndpoint reports whether a hosted server still points at the retired
// SSE endpoint of the same host. A genuinely custom URL is not touched.
func isLegacyEndpoint(server config.MCPServer, entry hub.Entry) bool {
	if server.Command != "" || server.URL == entry.URL {
		return false
	}
	return strings.TrimSuffix(server.URL, "/sse") == strings.TrimSuffix(entry.URL, "/mcp")
}

// pinnedArgs returns the server's arguments with the unpinned package reference
// swapped for the catalogue's pinned one, preserving every argument the user
// supplied after it. The second result is false when nothing needs upgrading,
// which also makes a second pass over an already-pinned config a no-op.
func pinnedArgs(server config.MCPServer, entry hub.Entry) ([]string, bool) {
	if server.Command != entry.Command || len(server.Args) == 0 || len(entry.Args) == 0 {
		return nil, false
	}
	switch server.Command {
	case "npx":
		// npx -y <package> [user args...]
		if server.Args[0] != "-y" || len(server.Args) < 2 || len(entry.Args) < 2 {
			return nil, false
		}
		if !strings.HasPrefix(entry.Args[1], server.Args[1]+"@") {
			return nil, false
		}
		out := append([]string(nil), server.Args...)
		out[1] = entry.Args[1]
		return out, true
	case "uvx":
		// uvx <package> [user args...] becomes
		// uvx --from <pinned package> --with <pinned sdk> <package> [user args...]
		if strings.HasPrefix(server.Args[0], "-") {
			return nil, false
		}
		exe := -1
		for i, arg := range entry.Args {
			if arg == server.Args[0] {
				exe = i
			}
		}
		if exe < 0 {
			return nil, false
		}
		out := append([]string(nil), entry.Args[:exe+1]...)
		return append(out, server.Args[1:]...), true
	}
	return nil, false
}

// Close shuts every server down and removes its tools from the registry.
func (m *Manager) Close() {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	m.mu.Lock()
	clients := m.clients
	m.clients = map[string]*Client{}
	m.errs = map[string]string{}
	if m.registry != nil {
		m.registry.Replace(m.registered, nil)
		m.registered = nil
	}
	m.mu.Unlock()

	for _, client := range clients {
		_ = client.Close()
	}
}

// ServerStatus reports one server for the dashboard.
type ServerStatus struct {
	Name      string    `json:"name"`
	Started   bool      `json:"started"`
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
			st.Started = true
			st.Connected, st.Error = c.ToolState()
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

// Register publishes every connected server's tools into the registry and keeps
// the registry binding for later refreshes.
func (m *Manager) Register(reg *tools.Registry) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.registry = reg
	replacements, registered := m.registryToolsLocked()
	reg.Replace(m.registered, replacements)
	m.registered = append([]string{}, registered...)
	return registered
}

// Refresh reconnects every configured server, atomically replaces the MCP
// tools visible to agents, and then closes the old transports.
func (m *Manager) Refresh(ctx context.Context, cfg *config.Config) []ServerStatus {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	clients, errs := connectAll(ctx, cfg)

	m.mu.Lock()
	oldClients := m.clients
	m.clients = clients
	m.errs = errs
	if m.registry != nil {
		replacements, registered := m.registryToolsLocked()
		m.registry.Replace(m.registered, replacements)
		m.registered = append([]string{}, registered...)
	}
	m.mu.Unlock()

	for _, client := range oldClients {
		_ = client.Close()
	}
	return m.Status(cfg)
}

func (m *Manager) registryToolsLocked() ([]tools.Tool, []string) {
	var replacements []tools.Tool
	var registered []string
	for serverName, client := range m.clients {
		ready, _ := client.ToolState()
		if !ready {
			continue
		}
		for _, def := range client.Tools() {
			t := &remoteTool{
				name:   mcpToolName(serverName, def.Name),
				server: serverName,
				remote: def.Name,
				desc:   def.Description,
				schema: def.InputSchema,
				client: client,
			}
			replacements = append(replacements, t)
			registered = append(registered, t.name)
		}
	}
	// One manager-level tool surfaces resources across every started server. The
	// mcp__ prefix means the toolset resolver includes it opt-out, like the
	// remote tools.
	if len(m.clients) > 0 {
		replacements = append(replacements, &resourceTool{m: m})
		registered = append(registered, "mcp__resource")
	}
	sort.Strings(registered)
	return replacements, registered
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
