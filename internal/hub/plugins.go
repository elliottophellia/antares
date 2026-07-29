package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PluginEntry is one catalogue plugin. Unlike skills (prose) or MCP servers
// (a command reference), a plugin is an executable that runs on this machine,
// so the whole thing ships inside the binary: the manifest and the script are
// bundled here rather than fetched. Nothing is downloaded at install time — the
// operator sees exactly what will run before saying yes.
type PluginEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Author      string   `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Hooks       []string `json:"hooks"`
	TimeoutMS   int      `json:"timeout_ms,omitempty"`
	Description string   `json:"description,omitempty"`
	// ScriptName + Script is the bundled executable dropped alongside the
	// manifest. Empty for a plugin that only shells out to a tool on PATH.
	ScriptName string `json:"script_name,omitempty"`
	Script     string `json:"script,omitempty"`
	// Installed is filled in by the caller from local state.
	Installed bool `json:"installed"`
}

var pluginCache []PluginEntry

func loadPlugins() {
	load() // reuse the skills/mcp once-guard's embed handle
	if pluginCache != nil {
		return
	}
	if raw, err := bundled.ReadFile("catalog/plugins.json"); err == nil {
		var list []PluginEntry
		if json.Unmarshal(raw, &list) == nil {
			pluginCache = list
		}
	}
	if pluginCache == nil {
		pluginCache = []PluginEntry{}
	}
}

// PluginCatalogue returns the plugins that ship with the binary.
func PluginCatalogue() []PluginEntry {
	loadPlugins()
	out := make([]PluginEntry, len(pluginCache))
	copy(out, pluginCache)
	return out
}

// SearchPlugins finds catalogue plugins matching a query, marking the ones
// already present on disk.
func SearchPlugins(_ context.Context, query string, installed map[string]bool) []PluginEntry {
	q := strings.TrimSpace(strings.ToLower(query))
	out := make([]PluginEntry, 0, len(PluginCatalogue()))
	for _, e := range PluginCatalogue() {
		if q != "" {
			hay := strings.ToLower(strings.Join([]string{e.Name, e.Summary, e.ID, strings.Join(e.Tags, " ")}, " "))
			match := true
			for _, word := range strings.Fields(q) {
				if !strings.Contains(hay, word) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		e.Installed = installed[e.Name]
		out = append(out, e)
	}
	return out
}

// LookupPlugin returns one catalogue plugin.
func LookupPlugin(id string) (PluginEntry, bool) {
	for _, e := range PluginCatalogue() {
		if e.ID == id || e.Name == id {
			return e, true
		}
	}
	return PluginEntry{}, false
}

// InstallPlugin writes a catalogue plugin's manifest (and bundled script, if
// any) into dir/<name>/. It returns the directory it wrote to. The caller is
// expected to have shown the command to the operator first — a plugin runs
// code, so installing one is a decision, not a convenience.
func InstallPlugin(id, dir string) (PluginEntry, string, error) {
	entry, ok := LookupPlugin(id)
	if !ok {
		return PluginEntry{}, "", fmt.Errorf("no plugin named %q in the catalogue", id)
	}
	dest := filepath.Join(dir, entry.Name)
	if _, err := os.Stat(dest); err == nil {
		return PluginEntry{}, "", fmt.Errorf("%s is already installed", entry.Name)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return PluginEntry{}, "", err
	}

	man := map[string]any{
		"name":        entry.Name,
		"description": entry.Description,
		"command":     entry.Command,
	}
	if len(entry.Args) > 0 {
		man["args"] = entry.Args
	}
	if len(entry.Hooks) > 0 {
		man["hooks"] = entry.Hooks
	}
	if entry.TimeoutMS > 0 {
		man["timeout_ms"] = entry.TimeoutMS
	}
	out, err := yaml.Marshal(man)
	if err != nil {
		return PluginEntry{}, "", err
	}
	if err := os.WriteFile(filepath.Join(dest, "plugin.yaml"), out, 0o644); err != nil {
		return PluginEntry{}, "", err
	}
	// Drop the bundled script and make it runnable. A relative command in the
	// manifest resolves against the plugin directory, so this is what runs.
	if entry.ScriptName != "" && entry.Script != "" {
		if err := os.WriteFile(filepath.Join(dest, entry.ScriptName), []byte(entry.Script), 0o755); err != nil {
			return PluginEntry{}, "", err
		}
	}
	return entry, dest, nil
}
