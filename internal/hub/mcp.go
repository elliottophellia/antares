package hub

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// SearchMCP finds MCP servers in the catalogue, marking the ones already
// configured so the UI does not offer to install them twice.
func SearchMCP(_ context.Context, query string, cfg *config.Config) []Entry {
	out := make([]Entry, 0, len(MCPCatalogue()))
	for _, e := range MCPCatalogue() {
		if !matches(e, strings.TrimSpace(query)) {
			continue
		}
		if cfg != nil {
			_, e.Installed = cfg.MCP.Servers[e.ID]
		}
		out = append(out, e)
	}
	return out
}

// LookupMCP returns one catalogue entry.
func LookupMCP(id string) (Entry, bool) {
	for _, e := range MCPCatalogue() {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// InstallMCP adds a catalogue server to the configuration. Keys the server
// needs are taken from env when they are set there, so a machine that already
// exports GITHUB_PERSONAL_ACCESS_TOKEN needs no further setup.
//
// It returns the names of any keys still missing, which the caller shows to
// the user rather than leaving a server that fails silently on first use.
func InstallMCP(id string, cfg *config.Config, overrides map[string]string) ([]string, error) {
	entry, ok := LookupMCP(id)
	if !ok {
		return nil, fmt.Errorf("no server named %q in the catalogue", id)
	}
	if cfg.MCP.Servers == nil {
		cfg.MCP.Servers = map[string]config.MCPServer{}
	}
	if _, exists := cfg.MCP.Servers[id]; exists {
		return nil, fmt.Errorf("%s is already configured", id)
	}

	server := config.MCPServer{
		Enabled:   true,
		Transport: "stdio",
		Command:   entry.Command,
		Args:      append([]string(nil), entry.Args...),
		URL:       entry.URL,
	}
	if entry.URL != "" {
		// A hosted server has no command to run.
		server.Transport = "http"
	}
	if len(entry.Env) > 0 {
		server.Env = map[string]string{}
	}

	var missing []string
	for key := range entry.Env {
		value := overrides[key]
		if value == "" {
			value = strings.TrimSpace(os.Getenv(key))
		}
		if value == "" {
			missing = append(missing, key)
			continue
		}
		server.Env[key] = value
	}

	cfg.MCP.Servers[id] = server
	return missing, nil
}
