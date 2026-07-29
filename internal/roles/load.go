package roles

import (
	"os"
	"path/filepath"
	"strings"
)

// loadInto fills m from the bundled catalogue. dir is unused for the bundled
// case; it exists so Reload can share one path.
func (r *Registry) loadInto(m map[string]Role, _ string, _ bool) {
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
			m[role.Name] = role
		}
	}
}

// loadDir reads role files from one directory into m.
func loadDir(dir string, m map[string]Role) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if role, ok := parse(string(raw), "local"); ok {
			role.Path = path
			m[role.Name] = role
		}
	}
	return nil
}
