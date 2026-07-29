// Package hub is where new capabilities come from: a browsable catalogue of
// skills and MCP servers, and the machinery to install one.
//
// A curated set ships inside the binary so the hub is useful with no network
// and no account. Beyond that it reads plain GitHub repositories and any URL
// serving a SKILL.md, which means publishing to it needs nothing more than a
// public repo.
package hub

import (
	"embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog/skills/*.md catalog/mcp.json catalog/plugins.json
var bundled embed.FS

// Entry is one catalogue item, whether a skill or an MCP server.
type Entry struct {
	// ID is what an install command takes, e.g. "builtin/code-review" or
	// "github/owner/repo/path".
	ID       string   `json:"id"`
	Kind     string   `json:"kind"` // skill|mcp
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Source   string   `json:"source"` // builtin|github|url|registry
	Tags     []string `json:"tags,omitempty"`
	Homepage string   `json:"homepage,omitempty"`
	Author   string   `json:"author,omitempty"`
	// Installed is filled in by the caller that knows the local state.
	Installed bool `json:"installed"`

	// Skill-only.
	Body string `json:"-"`

	// MCP-only: how to run the server.
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	NeedsKeys []string          `json:"needs_keys,omitempty"`
	Setup     string            `json:"setup,omitempty"`
}

var (
	loadOnce   sync.Once
	skillCache []Entry
	mcpCache   []Entry
)

// load reads the embedded catalogue once.
func load() {
	loadOnce.Do(func() {
		entries, err := bundled.ReadDir("catalog/skills")
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				raw, err := bundled.ReadFile("catalog/skills/" + e.Name())
				if err != nil {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".md")
				meta := parseFrontMatter(string(raw))
				skillCache = append(skillCache, Entry{
					ID:      "builtin/" + name,
					Kind:    "skill",
					Name:    name,
					Summary: meta.Description,
					Source:  "builtin",
					Tags:    meta.Tags,
					Body:    string(raw),
				})
			}
			sort.Slice(skillCache, func(i, j int) bool { return skillCache[i].Name < skillCache[j].Name })
		}

		if raw, err := bundled.ReadFile("catalog/mcp.json"); err == nil {
			var list []Entry
			if json.Unmarshal(raw, &list) == nil {
				for i := range list {
					list[i].Kind = "mcp"
					list[i].Source = "builtin"
					if list[i].ID == "" {
						list[i].ID = list[i].Name
					}
				}
				mcpCache = list
			}
		}
	})
}

// BundledSkills returns the skills that ship with the binary.
func BundledSkills() []Entry {
	load()
	out := make([]Entry, len(skillCache))
	copy(out, skillCache)
	return out
}

// MCPCatalogue returns the known MCP servers.
func MCPCatalogue() []Entry {
	load()
	out := make([]Entry, len(mcpCache))
	copy(out, mcpCache)
	return out
}

// matches reports whether an entry answers a free-text query.
func matches(e Entry, q string) bool {
	if q == "" {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{
		e.Name, e.Summary, strings.Join(e.Tags, " "), e.ID, e.Author,
	}, " "))
	for _, word := range strings.Fields(strings.ToLower(q)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

// frontMatter is the subset of a skill header the catalogue needs.
type frontMatter struct {
	Name        string
	Description string
	Tags        []string
}

// parseFrontMatter reads the YAML header without pulling in a parser: the
// catalogue only needs three scalar fields and a simple list.
func parseFrontMatter(text string) frontMatter {
	var fm frontMatter
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return fm
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return fm
	}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "name":
			fm.Name = value
		case "description":
			fm.Description = value
		case "tags":
			value = strings.Trim(value, "[]")
			for _, t := range strings.Split(value, ",") {
				if t = strings.Trim(strings.TrimSpace(t), `"'`); t != "" {
					fm.Tags = append(fm.Tags, t)
				}
			}
		}
	}
	return fm
}
