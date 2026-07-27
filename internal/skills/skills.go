// Package skills manages the agent's learned procedures: Markdown files with
// YAML front matter that are injected into the prompt when relevant.
package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Skill is one learned procedure loaded from disk.
type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Path        string    `json:"path"`
	Enabled     bool      `json:"enabled"`
	Source      string    `json:"source"`
	Tags        []string  `json:"tags"`
	Triggers    []string  `json:"triggers"`
	Category    string    `json:"category,omitempty"`
	Body        string    `json:"-"`
	UpdatedAt   time.Time `json:"updated_at"`
	UsageCount  int       `json:"usage_count"`
	// Pack marks a skill from the bundled security library: searchable and
	// loadable, but kept out of the prompt catalogue so thousands of them do
	// not bury the conversation.
	Pack bool `json:"pack,omitempty"`
}

// frontMatter is the YAML header of a skill file.
type frontMatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Enabled     *bool    `yaml:"enabled"`
	Source      string   `yaml:"source"`
	Category    string   `yaml:"category"`
	Tags        []string `yaml:"tags"`
	Triggers    []string `yaml:"triggers"`
}

// Manager loads and caches skills from the configured directories.
type Manager struct {
	mu       sync.RWMutex
	dirs     []string
	packDirs []string
	skills   map[string]*Skill
	usage    map[string]int
}

// NewManager builds a manager over the given directories.
func NewManager(dirs []string) *Manager {
	return &Manager{dirs: dirs, skills: map[string]*Skill{}, usage: map[string]int{}}
}

// SetPackDirs marks directories whose skills are the bundled security library:
// searchable but not in the prompt catalogue.
func (m *Manager) SetPackDirs(dirs []string) {
	m.mu.Lock()
	m.packDirs = dirs
	m.mu.Unlock()
}

// isPack reports whether a path is under a pack directory.
func (m *Manager) isPack(path string) bool {
	for _, d := range m.packDirs {
		if d != "" && strings.HasPrefix(path, d) {
			return true
		}
	}
	return false
}

// Reload rescans every configured directory.
func (m *Manager) Reload() error {
	found := map[string]*Skill{}
	var firstErr error

	for _, dir := range m.dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil && firstErr == nil {
			firstErr = err
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ".md") {
				return nil
			}
			s, err := parseFile(path)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return nil
			}
			// Later directories win, letting a user copy override a bundled skill.
			found[s.Name] = s
			return nil
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	m.mu.Lock()
	for name, sk := range found {
		sk.UsageCount = m.usage[name]
		sk.Pack = m.isPack(sk.Path)
	}
	m.skills = found
	m.mu.Unlock()
	return firstErr
}

// parseFile reads one skill file, tolerating a missing front matter block.
func parseFile(path string) (*Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	s := &Skill{
		Path:    path,
		Enabled: true,
		Source:  "local",
		Name:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
	}
	if fi, err := os.Stat(path); err == nil {
		s.UpdatedAt = fi.ModTime()
	}

	body := text
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			header := text[4 : 4+end]
			body = strings.TrimPrefix(text[4+end+4:], "\n")

			var fm frontMatter
			if err := yaml.Unmarshal([]byte(header), &fm); err != nil {
				return nil, fmt.Errorf("%s: invalid front matter: %w", path, err)
			}
			if fm.Name != "" {
				s.Name = fm.Name
			}
			s.Description = fm.Description
			s.Tags, s.Triggers = fm.Tags, fm.Triggers
			s.Category = fm.Category
			if fm.Source != "" {
				s.Source = fm.Source
			}
			if fm.Enabled != nil {
				s.Enabled = *fm.Enabled
			}
		}
	}
	s.Body = strings.TrimSpace(body)

	if s.Description == "" {
		// Fall back to the first non-heading line so the list is never blank.
		for _, line := range strings.Split(s.Body, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				s.Description = truncate(line, 160)
				break
			}
		}
	}
	return s, nil
}

// Everyday returns the skills for the everyday catalogue — everything except
// the bundled library, which is thousands strong and reached by search.
func (m *Manager) Everyday() []Skill {
	out := m.List()
	kept := make([]Skill, 0, len(out))
	for _, s := range out {
		if !s.Pack {
			kept = append(kept, s)
		}
	}
	return kept
}

// List returns all known skills, sorted by name.
func (m *Manager) List() []Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Skill, 0, len(m.skills))
	for _, s := range m.skills {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one skill by name.
func (m *Manager) Get(name string) (*Skill, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.skills[name]
	if !ok {
		return nil, false
	}
	cp := *s
	return &cp, true
}

// SetEnabled toggles a skill by rewriting its front matter.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	s, ok := m.Get(name)
	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	value := "false"
	if enabled {
		value = "true"
	}
	switch {
	case strings.HasPrefix(text, "---\n"):
		end := strings.Index(text[4:], "\n---")
		if end < 0 {
			return errors.New("front matter is not terminated")
		}
		header := text[4 : 4+end]
		rest := text[4+end:]
		if strings.Contains(header, "enabled:") {
			lines := strings.Split(header, "\n")
			for i, l := range lines {
				if strings.HasPrefix(strings.TrimSpace(l), "enabled:") {
					lines[i] = "enabled: " + value
				}
			}
			header = strings.Join(lines, "\n")
		} else {
			header += "\nenabled: " + value
		}
		text = "---\n" + header + rest
	default:
		text = "---\nname: " + s.Name + "\nenabled: " + value + "\n---\n\n" + text
	}

	if err := os.WriteFile(s.Path, []byte(text), 0o644); err != nil {
		return err
	}
	return m.Reload()
}

// Save writes (or overwrites) a skill file in the first configured directory.
func (m *Manager) Save(name, description, body string, tags []string) (*Skill, error) {
	if len(m.dirs) == 0 {
		return nil, errors.New("no skills directory configured")
	}
	name = sanitizeName(name)
	if name == "" {
		return nil, errors.New("skill name is required")
	}
	dir := m.dirs[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	header := frontMatter{Name: name, Description: description, Source: "agent", Tags: tags}
	headerYAML, err := yaml.Marshal(header)
	if err != nil {
		return nil, err
	}
	content := "---\n" + string(headerYAML) + "---\n\n" + strings.TrimSpace(body) + "\n"

	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	s, _ := m.Get(name)
	return s, nil
}

// Delete removes a skill file.
func (m *Manager) Delete(name string) error {
	s, ok := m.Get(name)
	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}
	if err := os.Remove(s.Path); err != nil {
		return err
	}
	return m.Reload()
}

// MarkUsed increments the in-memory usage counter shown in the dashboard.
func (m *Manager) MarkUsed(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage[name]++
	if s, ok := m.skills[name]; ok {
		s.UsageCount = m.usage[name]
	}
}

// PromptBlock renders the enabled skills as a compact catalogue for the system
// prompt: name, description, and triggers only. Full bodies are fetched on
// demand with the skill tool so the prompt stays small.
func (m *Manager) PromptBlock(limit int) string {
	list := m.List()
	var b strings.Builder
	n := 0
	for _, s := range list {
		if !s.Enabled || s.Pack {
			continue
		}
		if limit > 0 && n >= limit {
			break
		}
		fmt.Fprintf(&b, "- %s: %s", s.Name, s.Description)
		if len(s.Triggers) > 0 {
			fmt.Fprintf(&b, " (use when: %s)", strings.Join(s.Triggers, "; "))
		}
		b.WriteString("\n")
		n++
	}
	return b.String()
}

// Search finds skills by keyword across name, description, and tags. This is how
// the security library is used: not injected, but reached for on demand.
func (m *Manager) Search(query string, limit int) []Skill {
	q := strings.ToLower(strings.TrimSpace(query))
	list := m.List()
	if limit <= 0 {
		limit = 30
	}
	var out []Skill
	for _, s := range list {
		hay := strings.ToLower(s.Name + " " + s.Description + " " + strings.Join(s.Tags, " ") + " " + strings.Join(s.Triggers, " "))
		if q == "" || matchesAll(hay, q) {
			out = append(out, s)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// matchesAll reports whether every word of the query is in the haystack.
func matchesAll(hay, query string) bool {
	for _, w := range strings.Fields(query) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Categories lists the pack skill categories with their counts, for browsing.
func (m *Manager) Categories() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]int{}
	for _, s := range m.skills {
		if !s.Pack {
			continue
		}
		cat := s.Category
		if cat == "" {
			cat = "uncategorised"
		}
		out[cat]++
	}
	return out
}

// Library returns pack skills for browsing: filtered by category when given,
// sorted by name, and paged. It returns the page and the total in that filter.
func (m *Manager) Library(category string, offset, limit int) ([]Skill, int) {
	if limit <= 0 {
		limit = 50
	}
	m.mu.RLock()
	all := make([]Skill, 0)
	for _, s := range m.skills {
		if !s.Pack {
			continue
		}
		if category != "" && !strings.EqualFold(s.Category, category) {
			continue
		}
		all = append(all, *s)
	}
	m.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	total := len(all)
	if offset >= total {
		return nil, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total
}

// PackCount reports how many skills came from the bundled library.
func (m *Manager) PackCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, s := range m.skills {
		if s.Pack {
			n++
		}
	}
	return n
}

// Count reports how many skills are enabled.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, s := range m.skills {
		if s.Enabled {
			n++
		}
	}
	return n
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
