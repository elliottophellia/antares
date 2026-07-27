package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreModifiedFile(t *testing.T) {
	work := t.TempDir()
	s := NewStore(t.TempDir())

	path := filepath.Join(work, "config.yaml")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.Save("sess1", path, "write_file"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mangled\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.Restore("sess1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 1 {
		t.Fatalf("restored %v", res.Restored)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "original\n" {
		t.Fatalf("file is %q", got)
	}
}

func TestRestoreDeletesFilesThatDidNotExist(t *testing.T) {
	work := t.TempDir()
	s := NewStore(t.TempDir())

	path := filepath.Join(work, "new.txt")
	// Saved before it existed, which is what a create looks like.
	if err := s.Save("sess1", path, "write_file"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.Restore("sess1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("deleted %v", res.Deleted)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a file that did not exist before was left behind")
	}
}

func TestFirstSaveWins(t *testing.T) {
	work := t.TempDir()
	s := NewStore(t.TempDir())
	path := filepath.Join(work, "a.txt")

	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = s.Save("sess1", path, "write_file")
	_ = os.WriteFile(path, []byte("v2\n"), 0o644)
	// A second save must not overwrite the original copy, or rolling back
	// would only undo the most recent edit.
	_ = s.Save("sess1", path, "edit_file")
	_ = os.WriteFile(path, []byte("v3\n"), 0o644)

	if _, err := s.Restore("sess1", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "v1\n" {
		t.Fatalf("rolled back to %q, want the state before the session", got)
	}
}

func TestRestoreOneFileOnly(t *testing.T) {
	work := t.TempDir()
	s := NewStore(t.TempDir())
	a := filepath.Join(work, "a.txt")
	b := filepath.Join(work, "b.txt")

	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("before\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = s.Save("sess1", p, "write_file")
		_ = os.WriteFile(p, []byte("after\n"), 0o644)
	}

	if _, err := s.Restore("sess1", []string{a}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(a); string(got) != "before\n" {
		t.Fatalf("a is %q", got)
	}
	if got, _ := os.ReadFile(b); string(got) != "after\n" {
		t.Fatalf("b was restored when only a was asked for: %q", got)
	}
}

func TestRestoreWithNothingSaved(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Restore("never-ran", nil); err == nil {
		t.Fatal("expected an error when there is nothing to restore")
	}
}

func TestSessionsAreSeparate(t *testing.T) {
	work := t.TempDir()
	s := NewStore(t.TempDir())
	path := filepath.Join(work, "a.txt")
	_ = os.WriteFile(path, []byte("one\n"), 0o644)

	_ = s.Save("sess1", path, "write_file")
	_ = os.WriteFile(path, []byte("two\n"), 0o644)
	_ = s.Save("sess2", path, "write_file")
	_ = os.WriteFile(path, []byte("three\n"), 0o644)

	// Rolling back the second session goes to what the second session found.
	if _, err := s.Restore("sess2", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "two\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPathTraversalIsContained(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	work := t.TempDir()
	path := filepath.Join(work, "a.txt")
	_ = os.WriteFile(path, []byte("x\n"), 0o644)

	// A session id that tries to escape must land inside the root anyway.
	if err := s.Save("../../etc", path, "write_file"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one directory under the root, got %d", len(entries))
	}
	if name := entries[0].Name(); name != "______etc" {
		t.Fatalf("session directory is %q", name)
	}
}

func TestPrune(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	work := t.TempDir()
	path := filepath.Join(work, "a.txt")
	_ = os.WriteFile(path, []byte("x\n"), 0o644)
	_ = s.Save("old", path, "write_file")

	// Nothing is old enough yet.
	if n, err := s.Prune(time.Hour); err != nil || n != 0 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	// Everything is, with a zero cutoff.
	if n, err := s.Prune(0); err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
}

func TestClear(t *testing.T) {
	s := NewStore(t.TempDir())
	work := t.TempDir()
	path := filepath.Join(work, "a.txt")
	_ = os.WriteFile(path, []byte("x\n"), 0o644)
	_ = s.Save("sess1", path, "write_file")

	if err := s.Clear("sess1"); err != nil {
		t.Fatal(err)
	}
	cp, err := s.Load("sess1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.Entries) != 0 {
		t.Fatalf("clearing left %d entries", len(cp.Entries))
	}
}
