package skills

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type sourceKind uint8

const (
	sourcePack sourceKind = iota
	sourceUser
	sourceProject
	sourceConfigured
)

type sourceRoot struct {
	path string
	kind sourceKind
}

var userSkillRoots = [][]string{
	{".agent", "skills"},
	{".agents", "skills"},
	{".claude", "skills"},
	{".codex", "skills"},
	{".config", "opencode", "skills"},
	{".omp", "agent", "managed-skills"},
}

var projectSkillRoots = [][]string{
	{".agent", "skills"},
	{".agents", "skills"},
	{".claude", "skills"},
	{".codex", "skills"},
	{".opencode", "skills"},
	{".github", "skills"},
}

// discover scans roots from low to high priority. Later occurrences of a name
// replace earlier ones, while malformed entries leave the rest of the freshly
// discovered catalogue available.
func discover(opts Options) (map[string]*Skill, error) {
	found := make(map[string]*Skill)
	var firstErr error
	for _, root := range discoveryRoots(opts) {
		if err := scanRoot(root, func(skill *Skill) {
			found[skill.Name] = skill
		}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return found, firstErr
}

func discoveryRoots(opts Options) []sourceRoot {
	roots := make([]sourceRoot, 0, len(opts.PackDirs)+len(opts.Dirs)+12)
	roots = appendSourceRoots(roots, opts.PackDirs, sourcePack)
	if strings.TrimSpace(opts.UserHome) != "" {
		paths := make([]string, 0, len(userSkillRoots))
		for _, parts := range userSkillRoots {
			paths = append(paths, filepath.Join(append([]string{opts.UserHome}, parts...)...))
		}
		roots = appendSourceRoots(roots, paths, sourceUser)
	}
	if strings.TrimSpace(opts.ProjectDir) != "" {
		paths := make([]string, 0, len(projectSkillRoots))
		for _, parts := range projectSkillRoots {
			paths = append(paths, filepath.Join(append([]string{opts.ProjectDir}, parts...)...))
		}
		roots = appendSourceRoots(roots, paths, sourceProject)
	}
	return appendSourceRoots(roots, opts.Dirs, sourceConfigured)
}

// appendSourceRoots drops equivalent roots only within one source kind. Walking
// backwards preserves the last, highest-priority spelling of each root.
func appendSourceRoots(dst []sourceRoot, paths []string, kind sourceKind) []sourceRoot {
	kept := make([]string, 0, len(paths))
	for i := len(paths) - 1; i >= 0; i-- {
		path := paths[i]
		if strings.TrimSpace(path) == "" {
			continue
		}
		logical, err := filepath.Abs(path)
		if err != nil {
			logical = filepath.Clean(path)
		}
		duplicate := false
		for _, prior := range kept {
			if sameRoot(logical, prior) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		kept = append(kept, logical)
	}
	for i := len(kept) - 1; i >= 0; i-- {
		dst = append(dst, sourceRoot{path: kept[i], kind: kind})
	}
	return dst
}

func sameRoot(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	return left == right
}

func scanRoot(root sourceRoot, publish func(*Skill)) error {
	var firstErr error
	ancestors := make(map[string]struct{})
	var walk func(string)
	walk = func(logical string) {
		info, err := os.Stat(logical)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
			return
		}
		if info.IsDir() {
			canonical, err := filepath.EvalSymlinks(logical)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
					firstErr = err
				}
				return
			}
			canonical, err = filepath.Abs(canonical)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			if _, cycle := ancestors[canonical]; cycle {
				return
			}
			ancestors[canonical] = struct{}{}
			entries, err := os.ReadDir(logical)
			if err != nil {
				delete(ancestors, canonical)
				if !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
					firstErr = err
				}
				return
			}
			for _, entry := range entries { // os.ReadDir returns lexical order.
				if strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				walk(filepath.Join(logical, entry.Name()))
			}
			delete(ancestors, canonical)
			return
		}
		if !info.Mode().IsRegular() {
			return
		}
		base := filepath.Base(logical)
		if root.kind == sourceUser || root.kind == sourceProject {
			if !strings.EqualFold(base, "SKILL.md") {
				return
			}
		} else if !strings.EqualFold(filepath.Ext(base), ".md") {
			return
		}

		skill, err := parseFile(logical)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		if strings.TrimSpace(skill.Name) == "" {
			if strings.EqualFold(base, "SKILL.md") {
				skill.Name = filepath.Base(filepath.Dir(logical))
			} else {
				skill.Name = strings.TrimSuffix(base, filepath.Ext(base))
			}
		}
		if skill.Source == "" {
			skill.Source = "local"
		}
		skill.Path = logical
		skill.UpdatedAt = info.ModTime()
		skill.Pack = root.kind == sourcePack
		skill.ReadOnly = root.kind == sourceUser || root.kind == sourceProject
		publish(skill)
	}
	walk(root.path)
	return firstErr
}
