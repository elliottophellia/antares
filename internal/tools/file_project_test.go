package tools

import (
	"path/filepath"
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
