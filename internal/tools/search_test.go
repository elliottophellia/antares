package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grep and glob must follow the same read boundary as read_file/list_files:
// confined to the workspace in an ordinary session, free to search anywhere in
// a project session (WriteRoots set).
func TestGrepAndGlobFollowProjectReadBoundary(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "ref.txt"), []byte("needle content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	grepArgs, _ := json.Marshal(map[string]any{"pattern": "needle", "path": outside})
	projectIn := Input{Workspace: project, WriteRoots: []string{project}, Args: grepArgs}
	result := (grepTool{}).Execute(context.Background(), projectIn)
	if result.IsError || !strings.Contains(result.Content, "needle content") {
		t.Fatalf("project-session grep outside workspace should match, got: %+v", result)
	}

	globArgs, _ := json.Marshal(map[string]any{"pattern": "*.txt", "path": outside})
	result = (globTool{}).Execute(context.Background(), Input{Workspace: project, WriteRoots: []string{project}, Args: globArgs})
	if result.IsError || !strings.Contains(result.Content, "ref.txt") {
		t.Fatalf("project-session glob outside workspace should match, got: %+v", result)
	}

	// Ordinary sessions keep the old confinement.
	result = (grepTool{}).Execute(context.Background(), Input{Workspace: project, Args: grepArgs})
	if !result.IsError {
		t.Fatalf("ordinary-session grep outside workspace must be refused, got: %+v", result)
	}
	result = (globTool{}).Execute(context.Background(), Input{Workspace: project, Args: globArgs})
	if !result.IsError {
		t.Fatalf("ordinary-session glob outside workspace must be refused, got: %+v", result)
	}
}

// A line longer than the scanner buffer used to abort the file scan: no matches
// after it, and no report that the search had given up. Reporting the early
// stop was the fix available while lines came through a fixed buffer; lines are
// now cut from the whole file, so there is no buffer to overrun and the rest of
// the file is searched and numbered normally.
func TestGrepSearchesPastAnOverlongLine(t *testing.T) {
	workspace := t.TempDir()
	content := strings.Repeat("x", 2*1024*1024) + "\nNEEDLE line\n"
	if err := os.WriteFile(filepath.Join(workspace, "big.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"pattern": "NEEDLE", "path": "."})
	result := (grepTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("grep errored: %s", result.Content)
	}
	if !strings.Contains(result.Content, "big.txt") || !strings.Contains(result.Content, "2:\tNEEDLE line") {
		t.Fatalf("the match after an overlong line was not reported at line 2: %q", result.Content)
	}
	if strings.Contains(result.Content, "warning") {
		t.Fatalf("the whole file was searched, so nothing should be warned about: %q", result.Content)
	}
}
