// Package roles is the agent's cast of specialists.
//
// One general assistant is a generalist by necessity, and a generalist is
// mediocre at everything. A role is a named specialist: its own instructions,
// its own set of tools, and optionally its own model — a reviewer that only
// reads, a researcher that only browses, a report writer that only writes.
//
// Roles are how one conversation becomes a team. The primary agent delegates a
// self-contained piece of work to the role suited to it, and that role runs
// with exactly the reach it needs and no more.
package roles

import (
	"embed"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed catalog/*.md
var bundled embed.FS

// Role is one specialist.
type Role struct {
	// Name is how the role is addressed: kebab-case, unique.
	Name string `json:"name"`
	// Title is the human label.
	Title string `json:"title"`
	// Summary is one line: what this role is for.
	Summary string `json:"summary"`
	// Category groups roles in the picker — "general", "engineering",
	// "security", and so on.
	Category string `json:"category"`
	// Toolset is which tools the role runs with. Empty inherits the default.
	Toolset string `json:"toolset"`
	// Model overrides the model for this role. Empty inherits.
	Model string `json:"model"`
	// Tags aid discovery.
	Tags []string `json:"tags,omitempty"`
	// Danger marks a role whose work needs authorisation to be lawful —
	// security testing against a target you do not own. The UI warns; the
	// prompt insists on scope.
	Danger bool `json:"danger,omitempty"`
	// Effort overrides the reasoning effort for this role (low|medium|high|…).
	// Empty inherits. A reviewer can think hard; a formatter need not.
	Effort string `json:"effort,omitempty"`
	// MaxTurns caps a delegated run of this role. Zero inherits the default.
	MaxTurns int `json:"max_turns,omitempty"`
	// Prompt is the role's standing instructions, appended to the system
	// prompt when the role is active.
	Prompt string `json:"-"`
	// Source is "builtin" or "local".
	Source string `json:"source"`
}

// frontMatter is the YAML header of a role file.
type frontMatter struct {
	Name     string   `yaml:"name"`
	Title    string   `yaml:"title"`
	Summary  string   `yaml:"summary"`
	Category string   `yaml:"category"`
	Toolset  string   `yaml:"toolset"`
	Model    string   `yaml:"model"`
	Effort   string   `yaml:"effort"`
	MaxTurns int      `yaml:"max_turns"`
	Tags     []string `yaml:"tags"`
	Danger   bool     `yaml:"danger"`
}

// Registry holds the roles available to a process.
type Registry struct {
	mu    sync.RWMutex
	roles map[string]Role
	dirs  []string
}

// NewRegistry builds a registry that reads the bundled roles plus any in the
// given directories. Directory roles override bundled ones by name, so a user
// can adjust a shipped role by dropping a file with the same name.
func NewRegistry(dirs []string) *Registry {
	r := &Registry{roles: map[string]Role{}, dirs: dirs}
	r.loadBundled()
	return r
}

func (r *Registry) loadBundled() {
	entries, err := bundled.ReadDir("catalog")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := bundled.ReadFile("catalog/" + e.Name())
		if err != nil {
			continue
		}
		if role, ok := parse(string(raw), "builtin"); ok {
			r.roles[role.Name] = role
		}
	}
}

// Get returns one role.
func (r *Registry) Get(name string) (Role, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	role, ok := r.roles[strings.ToLower(strings.TrimSpace(name))]
	return role, ok
}

// List returns every role, sorted by category then name.
func (r *Registry) List() []Role {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Role, 0, len(r.roles))
	for _, role := range r.roles {
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryRank(out[i].Category) < categoryRank(out[j].Category)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Count is how many roles are loaded.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.roles)
}

// parse reads one role file. A file without front matter is not a role.
func parse(text, source string) (Role, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Role{}, false
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return Role{}, false
	}
	var fm frontMatter
	if yaml.Unmarshal([]byte(text[4:4+end]), &fm) != nil {
		return Role{}, false
	}
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		return Role{}, false
	}
	body := strings.TrimSpace(text[4+end+4:])

	title := fm.Title
	if title == "" {
		title = humanize(name)
	}
	cat := fm.Category
	if cat == "" {
		cat = "general"
	}
	return Role{
		Name:     strings.ToLower(name),
		Title:    title,
		Summary:  fm.Summary,
		Category: cat,
		Toolset:  fm.Toolset,
		Model:    fm.Model,
		Effort:   fm.Effort,
		MaxTurns: fm.MaxTurns,
		Tags:     fm.Tags,
		Danger:   fm.Danger,
		Prompt:   body,
		Source:   source,
	}, true
}

// categoryRank keeps the general roles first and the specialised ones after.
func categoryRank(c string) int {
	switch c {
	case "general":
		return 0
	case "engineering":
		return 1
	case "research":
		return 2
	case "writing":
		return 3
	case "security":
		return 4
	default:
		return 5
	}
}

func humanize(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// Reload rescans the user directories, keeping the bundled roles and letting a
// directory role override one by name.
func (r *Registry) Reload() error {
	fresh := map[string]Role{}
	// Bundled first, so a user file with the same name wins.
	r.loadInto(fresh, "", true)
	for _, dir := range r.dirs {
		if err := loadDir(dir, fresh); err != nil {
			// A bad directory is not worth failing every role for.
			continue
		}
	}
	r.mu.Lock()
	r.roles = fresh
	r.mu.Unlock()
	return nil
}
