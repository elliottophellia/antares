package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Project sessions confine writes to WriteRoots while allowing reads anywhere.
// These are the boundary guarantees the feature rests on, so they get direct
// coverage rather than relying on a live end-to-end run.
func TestProjectWriteConfinement(t *testing.T) {
	project := t.TempDir()
	workspace := t.TempDir()
	outside := t.TempDir()

	in := Input{Workspace: project, WriteRoots: []string{project, workspace}}

	// Writes inside the project are allowed.
	if _, err := resolveWrite(in, filepath.Join(project, "src", "main.go")); err != nil {
		t.Fatalf("write inside project should be allowed, got %v", err)
	}
	// Writes inside the antares workspace are allowed.
	if _, err := resolveWrite(in, filepath.Join(workspace, "note.md")); err != nil {
		t.Fatalf("write inside workspace should be allowed, got %v", err)
	}
	// Writes anywhere else are refused.
	if _, err := resolveWrite(in, filepath.Join(outside, "evil.sh")); err == nil {
		t.Fatalf("write outside all roots must be refused")
	}
	// A traversal that escapes the project must be refused.
	if _, err := resolveWrite(in, filepath.Join(project, "..", "escape.txt")); err == nil {
		t.Fatalf("traversal escaping the project must be refused")
	}
}

func TestProjectReadAnywhere(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()

	in := Input{Workspace: project, WriteRoots: []string{project}}

	// A project session may resolve reads outside the project (to read/copy).
	if _, err := resolveRead(in, filepath.Join(outside, "reference.txt")); err != nil {
		t.Fatalf("project read outside should resolve, got %v", err)
	}
}

func TestOrdinarySessionStaysConfined(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()

	in := Input{Workspace: workspace} // no WriteRoots => ordinary session

	// An ordinary session must not resolve reads or writes outside the workspace,
	// exactly as before the project feature existed.
	if _, err := resolveWrite(in, filepath.Join(outside, "x.txt")); err == nil {
		t.Fatalf("ordinary write outside workspace must be refused")
	}
	if _, err := resolveRead(in, filepath.Join(outside, "x.txt")); err == nil {
		t.Fatalf("ordinary read outside workspace must be refused")
	}
}

func TestWriteFileCreatesEmptyFileAndParents(t *testing.T) {
	workspace := t.TempDir()
	args, err := json.Marshal(map[string]any{"path": "nested/empty.txt", "content": ""})
	if err != nil {
		t.Fatal(err)
	}
	result := (writeFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("write_file: %s", result.Content)
	}
	if !strings.Contains(result.Content, "0 bytes, 0 lines") {
		t.Fatalf("empty-file result = %q, want zero bytes and zero lines", result.Content)
	}
	path := filepath.Join(workspace, "nested", "empty.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("created empty file missing: %v", err)
	}
	if info.IsDir() || info.Size() != 0 {
		t.Fatalf("created empty target = %+v, want a zero-byte file", info)
	}
}

func TestWriteFileRejectsDirectoryTargetWithoutMutation(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "existing")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{"path": "existing", "content": "should not write"})
	if err != nil {
		t.Fatal(err)
	}
	result := (writeFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "target is a directory") {
		t.Fatalf("directory target result = %+v, want actionable error", result)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory was mutated: %v", entries)
	}
}

func TestWriteFileRejectsMissingContentBeforeFilesystemMutation(t *testing.T) {
	workspace := t.TempDir()
	result := (writeFileTool{}).Execute(context.Background(), Input{
		Workspace: workspace,
		Args:      []byte(`{"path":"nested/missing.txt"}`),
	})
	if !result.IsError || !strings.Contains(result.Content, "content is required") {
		t.Fatalf("missing-content result = %+v, want actionable error", result)
	}
	if _, err := os.Stat(filepath.Join(workspace, "nested")); !os.IsNotExist(err) {
		t.Fatalf("missing content mutated filesystem: %v", err)
	}
}
