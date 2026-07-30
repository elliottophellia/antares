package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/rag"
	"github.com/enowdev/antares/internal/tools"
)

// projectRAGIgnoreDirs are directories skipped when indexing a project.
var projectRAGIgnoreDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true, ".venv": true,
	"__pycache__": true, "target": true, ".next": true, "vendor": true, ".antares": true,
}

// projectRAGExts are the file types worth indexing for retrieval.
var projectRAGExts = map[string]bool{
	".md": true, ".txt": true, ".go": true, ".py": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".rs": true, ".java": true, ".rb": true, ".php": true,
	".c": true, ".h": true, ".cpp": true, ".cs": true, ".sh": true, ".yaml": true,
	".yml": true, ".toml": true, ".json": true, ".sql": true, ".html": true, ".css": true,
}

// collectProjectDocs walks a project directory (absolute, outside the antares
// workspace is fine) into RAG docs, skipping the noisy dirs and non-text files,
// and always including AGENTS.md / CLAUDE.md / README at the root even if large.
func collectProjectDocs(dir string) []tools.RAGDoc {
	var docs []tools.RAGDoc
	add := func(p string) {
		data, err := os.ReadFile(p)
		if err != nil || len(data) == 0 || len(data) > 2<<20 {
			return
		}
		rel, _ := filepath.Rel(dir, p)
		docs = append(docs, tools.RAGDoc{
			ID: rel, Path: rel, Content: string(data),
			Meta: map[string]any{"path": rel, "bytes": len(data)},
		})
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if projectRAGIgnoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if projectRAGExts[strings.ToLower(filepath.Ext(p))] {
			add(p)
		}
		if len(docs) >= 3000 {
			return filepath.SkipAll
		}
		return nil
	})
	return docs
}

// handleIndexProject indexes a project folder into its own RAG collection, for a
// project session. Gated behind the dashboard password (it reads a whole tree).
func (s *Server) handleIndexProject(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	provider := s.agent.RAG()
	if provider == nil {
		writeError(w, http.StatusBadRequest, errors.New("RAG is disabled — enable it in Settings first"))
		return
	}
	var body struct {
		Dir string `json:"dir"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	dir := filepath.Clean(strings.TrimSpace(body.Dir))
	if dir == "" || !filepath.IsAbs(dir) {
		writeError(w, http.StatusBadRequest, errors.New("an absolute project dir is required"))
		return
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("not a directory"))
		return
	}

	docs := collectProjectDocs(dir)
	if len(docs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"files": 0, "chunks": 0})
		return
	}
	collection := rag.ProjectCollection(dir)
	chunks, err := provider.Index(r.Context(), collection, docs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files": len(docs), "chunks": chunks, "collection": collection,
	})
}

// handleProjectGit reports git state for the project dir: branch, ahead/behind,
// changed files (porcelain), and the last commit. Absent git repo returns
// repo=false rather than an error, so the sidebar can say "not a git repo".
func (s *Server) handleProjectGit(w http.ResponseWriter, r *http.Request) {
	dir, ok := resolveProjectDir(w, r)
	if !ok {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"repo": false})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	git := func(args ...string) string {
		c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		out, _ := c.Output()
		return strings.TrimSpace(string(out))
	}

	branch := git("rev-parse", "--abbrev-ref", "HEAD")
	lastCommit := git("log", "-1", "--pretty=%h %s")

	// ahead/behind vs the upstream, when one is set.
	var ahead, behind int
	if lr := git("rev-list", "--left-right", "--count", "@{upstream}...HEAD"); lr != "" {
		parts := strings.Fields(lr)
		if len(parts) == 2 {
			behind = atoiSafe(parts[0])
			ahead = atoiSafe(parts[1])
		}
	}

	// Porcelain status: 2-char code + path. Split into staged/modified/untracked.
	type change struct {
		Path   string `json:"path"`
		Status string `json:"status"` // human tag: staged | modified | untracked | deleted
	}
	var changes []change
	for _, line := range strings.Split(git("status", "--porcelain"), "\n") {
		if len(line) < 4 {
			continue
		}
		// Porcelain v1: two status columns then the path. Take the code as the
		// first two chars and the path as the remainder with leading spaces
		// trimmed — robust to the single space git normally inserts.
		code := line[:2]
		path := strings.TrimLeft(line[2:], " ")
		// Renames appear as "old -> new"; show the new path.
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		tag := "modified"
		switch {
		case code == "??":
			tag = "untracked"
		case strings.ContainsRune(code, 'D'):
			tag = "deleted"
		case code[0] != ' ' && code[0] != '?':
			// A non-space in the first (index) column means it is staged.
			tag = "staged"
		}
		changes = append(changes, change{Path: path, Status: tag})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":    true,
		"branch":  branch,
		"ahead":   ahead,
		"behind":  behind,
		"last":    lastCommit,
		"changes": changes,
	})
}

// handleProjectScripts lists runnable scripts detected in the project:
// package.json "scripts" and Makefile targets. The sidebar renders them as
// quick-run chips (which ask the agent to run them — no direct execution here).
func (s *Server) handleProjectScripts(w http.ResponseWriter, r *http.Request) {
	dir, ok := resolveProjectDir(w, r)
	if !ok {
		return
	}
	type script struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Source  string `json:"source"` // "npm" | "make"
	}
	var out []script

	// package.json scripts.
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			names := make([]string, 0, len(pkg.Scripts))
			for k := range pkg.Scripts {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				out = append(out, script{Name: k, Command: "npm run " + k, Source: "npm"})
			}
		}
	}

	// Makefile targets (simple heuristic: lines like `target:` at column 0).
	if data, err := os.ReadFile(filepath.Join(dir, "Makefile")); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" || line[0] == '\t' || line[0] == '#' || line[0] == '.' || line[0] == ' ' {
				continue
			}
			colon := strings.IndexByte(line, ':')
			eq := strings.IndexByte(line, '=')
			// Skip variable assignments: "X = y" / "X ?= y" (an "=" before the
			// colon, or no colon) and "X := y" (":=", the "=" right after the
			// colon). A real target is "name:" with a colon not immediately
			// followed by "=", and no "=" before it.
			if colon <= 0 {
				continue
			}
			if eq >= 0 && eq < colon {
				continue // "X = y" or "X ?= y"
			}
			if colon+1 < len(line) && line[colon+1] == '=' {
				continue // "X := y"
			}
			name := strings.TrimSpace(line[:colon])
			if name == "" || strings.ContainsAny(name, " \t") || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, script{Name: name, Command: "make " + name, Source: "make"})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"scripts": out})
}

// handleProjectTree lists the entries of a directory inside the project for the
// sidebar's file tree. `sub` is a project-relative subpath (empty = root). It
// stays inside the project and skips the usual noise (.git, node_modules, …).
func (s *Server) handleProjectTree(w http.ResponseWriter, r *http.Request) {
	dir, ok := resolveProjectDir(w, r)
	if !ok {
		return
	}
	sub := strings.TrimSpace(r.URL.Query().Get("sub"))
	target := filepath.Join(dir, filepath.Clean("/"+sub)) // clean+anchor prevents ../ escape
	if !strings.HasPrefix(target, dir) {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
		return
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	skip := map[string]bool{".git": true, "node_modules": true, ".DS_Store": true, "dist": true, "build": true, ".next": true, "vendor": true, "__pycache__": true}
	type node struct {
		Name  string `json:"name"`
		Path  string `json:"path"` // project-relative
		IsDir bool   `json:"is_dir"`
	}
	out := make([]node, 0, len(entries))
	for _, e := range entries {
		if skip[e.Name()] {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(sub, e.Name()))
		out = append(out, node{Name: e.Name(), Path: rel, IsDir: e.IsDir()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
