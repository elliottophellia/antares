package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// handleGetSoul returns the agent's identity file (SOUL.md) plus whether it is
// still the unset default, for the Soul settings page.
func (s *Server) handleGetSoul(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"soul":  config.Soul(),
		"unset": config.SoulIsUnset(),
	})
}

// handleSaveSoul overwrites SOUL.md from the editor. An empty body resets it to
// the unset default, which re-arms the first-conversation identity interview.
func (s *Server) handleSaveSoul(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Soul string `json:"soul"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveSoul(body.Soul); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unset": config.SoulIsUnset()})
}

// handleBrowseDirs powers the project-session folder picker. Unlike the Files
// page (confined to the agent workspace), this browses directories anywhere on
// the machine so a project can live outside the antares workspace — hence it is
// gated behind the dashboard password like the other sensitive surfaces.
//
// It only ever lists directories (a project is a folder), and it never reads
// file contents. The optional `path` query selects the directory to list; empty
// or "~" starts at the user's home.
func (s *Server) handleBrowseDirs(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}

	target := strings.TrimSpace(r.URL.Query().Get("path"))
	home, _ := os.UserHomeDir()
	if target == "" || target == "~" {
		target = home
	}
	target = config.Expand(target)
	if !filepath.IsAbs(target) {
		// Relative input is resolved against home so the picker never leaks the
		// server's process working directory.
		target = filepath.Join(home, target)
	}
	target = filepath.Clean(target)

	entries, err := os.ReadDir(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	type dir struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	out := make([]dir, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			// Hidden directories add noise to a picker; project roots are rarely
			// dotfolders, and the user can still paste an exact path to reach one.
			continue
		}
		out = append(out, dir{Name: name, Path: filepath.Join(target, name)})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	parent := filepath.Dir(target)
	if parent == target {
		parent = "" // at filesystem root
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    target,
		"parent":  parent,
		"home":    home,
		"entries": out,
	})
}

// handleCompleteDir returns directory completions for a partially-typed path,
// powering the project picker's type-ahead. Given `path` it lists sibling
// directories of the final segment whose names share its prefix.
func (s *Server) handleCompleteDir(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}

	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	home, _ := os.UserHomeDir()
	if raw == "" {
		raw = home + string(filepath.Separator)
	}
	raw = config.Expand(raw)
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(home, raw)
	}

	// Split into the directory to scan and the prefix to match. A trailing
	// separator means "list everything in this dir".
	base := raw
	prefix := ""
	if !strings.HasSuffix(raw, string(filepath.Separator)) {
		base = filepath.Dir(raw)
		prefix = filepath.Base(raw)
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		// Not an error worth surfacing — the user is mid-type; just no matches.
		writeJSON(w, http.StatusOK, map[string]any{"matches": []string{}})
		return
	}
	lp := strings.ToLower(prefix)
	matches := make([]string, 0, 12)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if prefix == "" && strings.HasPrefix(name, ".") {
			continue
		}
		if lp != "" && !strings.HasPrefix(strings.ToLower(name), lp) {
			continue
		}
		matches = append(matches, filepath.Join(base, name))
		if len(matches) >= 20 {
			break
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return strings.ToLower(matches[i]) < strings.ToLower(matches[j])
	})
	writeJSON(w, http.StatusOK, map[string]any{"matches": matches})
}
