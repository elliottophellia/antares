package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newRepo makes a git repository with one commit, or skips when git is absent.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	return dir
}

func TestAvailable(t *testing.T) {
	repo := newRepo(t)
	if !Available(repo) {
		t.Fatal("a git repository should support worktrees")
	}
	// A plain directory is not a repository.
	if Available(t.TempDir()) {
		t.Fatal("a non-repository reported worktree support")
	}
}

func TestCreateAndRemoveClean(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	w, err := Create(ctx, repo, "coder")
	if err != nil {
		t.Fatal(err)
	}
	// The worktree is a real directory with the repo's file in it.
	if _, err := os.Stat(filepath.Join(w.Path, "README.md")); err != nil {
		t.Fatalf("the worktree is missing the repo's files: %v", err)
	}
	if w.Dirty(ctx) {
		t.Fatal("a fresh worktree should be clean")
	}
	// Cleanup removes an unchanged worktree.
	note := w.Cleanup(ctx)
	if note == "" {
		t.Fatal("cleanup said nothing")
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatal("an unchanged worktree was not removed")
	}
}

func TestDirtyWorktreeIsKept(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	w, err := Create(ctx, repo, "coder")
	if err != nil {
		t.Fatal(err)
	}
	// A change makes it dirty.
	if err := os.WriteFile(filepath.Join(w.Path, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !w.Dirty(ctx) {
		t.Fatal("a worktree with a new file should be dirty")
	}
	// Cleanup leaves a dirty worktree in place for review.
	w.Cleanup(ctx)
	if _, err := os.Stat(w.Path); err != nil {
		t.Fatal("a worktree with work in it was removed")
	}
	// Force removal cleans it up regardless.
	if err := w.Remove(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatal("force did not remove the worktree")
	}
}

func TestTwoWorktreesAreIndependent(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	a, err := Create(ctx, repo, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove(ctx, true)
	b, err := Create(ctx, repo, "b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Remove(ctx, true)

	if a.Path == b.Path {
		t.Fatal("two worktrees share a path")
	}
	// A change in one does not appear in the other — the whole point.
	if err := os.WriteFile(filepath.Join(a.Path, "only-a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Path, "only-a.txt")); !os.IsNotExist(err) {
		t.Fatal("a change in one worktree leaked into another")
	}
}

func TestCreateOutsideRepoFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := Create(context.Background(), t.TempDir(), "x"); err == nil {
		t.Fatal("creating a worktree outside a repository should fail")
	}
}
