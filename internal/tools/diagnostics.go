package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// diagnosticsTool runs the right checker for a file and reports the errors,
// which is what a language server delivers in practice: find the problems
// before running. It uses whatever the machine has — the Go toolchain, tsc,
// ruff, and so on — and falls back cleanly when none is installed.
type diagnosticsTool struct{}

func (diagnosticsTool) Name() string { return "diagnostics" }

func (diagnosticsTool) Description() string {
	return "Check a file or the project for errors using the language's own tools — the Go compiler, the " +
		"TypeScript checker, a Python linter, and so on. Run it after editing code to catch mistakes before " +
		"running anything. Returns the errors with their file and line."
}

func (diagnosticsTool) Schema() map[string]any {
	return schema(map[string]any{
		"path": prop("string", "A file to check, or a directory for the whole project. Defaults to the workspace."),
	})
}

// checker names a tool and how to run it for a language.
type checker struct {
	bin  string
	args func(path string, isDir bool) []string
}

// checkersFor returns the diagnostic tools to try for a path, most precise
// first. The first one that is installed wins.
func checkersFor(path string, isDir bool) []checker {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return []checker{{"go", func(p string, d bool) []string {
			target := "./..."
			if !d {
				target = p
			}
			return []string{"vet", target}
		}}}
	case ".ts", ".tsx", ".js", ".jsx":
		return []checker{{"tsc", func(string, bool) []string { return []string{"--noEmit"} }}}
	case ".py":
		return []checker{
			{"ruff", func(p string, d bool) []string { return []string{"check", p} }},
			{"pyflakes", func(p string, d bool) []string { return []string{p} }},
		}
	case ".rs":
		return []checker{{"cargo", func(string, bool) []string { return []string{"check", "--quiet"} }}}
	case ".sh":
		return []checker{{"shellcheck", func(p string, d bool) []string { return []string{p} }}}
	}
	if isDir {
		// A directory with a Go module gets vetted even without a file ext.
		return []checker{{"go", func(string, bool) []string { return []string{"vet", "./..."} }}}
	}
	return nil
}

func (diagnosticsTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Path string `json:"path"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}

	path := strings.TrimSpace(args.Path)
	if path == "" {
		path = in.Workspace
	}
	if path == "" {
		return Errorf("no path given and no workspace configured")
	}
	if !filepath.IsAbs(path) && in.Workspace != "" {
		path = filepath.Join(in.Workspace, path)
	}

	isDir := filepath.Ext(path) == ""
	list := checkersFor(path, isDir)
	if len(list) == 0 {
		return Text(fmt.Sprintf("No diagnostic tool is wired up for %s. Run the project's own check with the terminal.", filepath.Base(path)))
	}

	for _, c := range list {
		bin, err := exec.LookPath(c.bin)
		if err != nil {
			continue // not installed; try the next
		}
		runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		cmd := exec.CommandContext(runCtx, bin, c.args(path, isDir)...)
		// Run in the directory so relative imports and configs resolve.
		if isDir {
			cmd.Dir = path
		} else {
			cmd.Dir = filepath.Dir(path)
		}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err = cmd.Run()
		cancel()

		result := strings.TrimSpace(out.String())
		if err == nil && result == "" {
			return Text(fmt.Sprintf("No problems found by %s.", c.bin))
		}
		if runCtx.Err() != nil {
			return Errorf("%s did not finish in time", c.bin)
		}
		if result == "" {
			result = err.Error()
		}
		return Text(fmt.Sprintf("%s reported:\n\n%s", c.bin, truncateTool(result, 8000)))
	}

	// Name what would have worked, so the fix is obvious.
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.bin)
	}
	return Text(fmt.Sprintf("None of the checkers for this file are installed (%s). Install one, or run the project's own check with the terminal.",
		strings.Join(names, ", ")))
}
