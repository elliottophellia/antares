// Package worktree isolates a delegated agent in its own copy of a git
// repository, so two sub-agents working in parallel do not tread on each
// other's files.
//
// A git worktree is a second checkout sharing one object store: cheap to
// create, and its own working directory. A sub-agent given one can edit, build,
// and commit without the parent's tree or another sibling's tree changing under
// it. When the sub-agent is done, an unchanged worktree is removed; one with
// work in it is left for a person to look at.
//
// None of this needs anything but git. Where git is absent, or the workspace is
// not a repository, isolation is simply unavailable and the caller falls back
// to the shared workspace — reported, never fatal.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Worktree is one isolated checkout.
type Worktree struct {
	// Path is the working directory the agent should use.
	Path string
	// Branch is the throwaway branch the worktree is on.
	Branch string
	// repoRoot is the main checkout, needed to remove the worktree later.
	repoRoot string
}

// Available reports whether isolation can be used for a workspace: git is
// installed and the workspace is inside a repository.
func Available(workspace string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	return repoRoot(workspace) != ""
}

// Create makes a worktree off the workspace's repository. label is worked into
// the branch and directory names so a person can tell whose it is.
func Create(ctx context.Context, workspace, label string) (*Worktree, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git is not installed, so isolated worktrees are unavailable")
	}
	root := repoRoot(workspace)
	if root == "" {
		return nil, fmt.Errorf("%s is not inside a git repository", workspace)
	}

	name := "antares/" + sanitize(label)
	branch := fmt.Sprintf("%s-%d", name, time.Now().UnixMilli())
	// Worktrees live beside the repo, not inside it, so they do not show up as
	// untracked files in the parent.
	dir := filepath.Join(filepath.Dir(root), ".antares-worktrees", sanitize(label)+fmt.Sprintf("-%d", time.Now().UnixMilli()))

	// Base the worktree on the current HEAD.
	cmd := exec.CommandContext(ctx, "git", "-C", root, "worktree", "add", "-b", branch, dir, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("could not create a worktree: %s", strings.TrimSpace(string(out)))
	}
	return &Worktree{Path: dir, Branch: branch, repoRoot: root}, nil
}

// Dirty reports whether the worktree has uncommitted changes — the signal for
// whether there is work worth keeping.
func (w *Worktree) Dirty(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "git", "-C", w.Path, "status", "--porcelain").Output()
	if err != nil {
		// If we cannot tell, assume there is something worth keeping.
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

// Remove deletes the worktree and its throwaway branch. It is meant for a
// worktree with nothing worth keeping; force removes it regardless.
func (w *Worktree) Remove(ctx context.Context, force bool) error {
	args := []string{"-C", w.repoRoot, "worktree", "remove", w.Path}
	if force {
		args = append(args, "--force")
	}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("could not remove the worktree: %s", strings.TrimSpace(string(out)))
	}
	// Delete the branch too, so an abandoned run leaves nothing behind.
	_ = exec.CommandContext(ctx, "git", "-C", w.repoRoot, "branch", "-D", w.Branch).Run()
	return nil
}

// Cleanup removes an unchanged worktree and leaves a changed one, returning a
// sentence describing what it did for the delegating agent to relay.
func (w *Worktree) Cleanup(ctx context.Context) string {
	if w.Dirty(ctx) {
		return fmt.Sprintf("The sub-agent's work is on branch %s in %s — review it there.", w.Branch, w.Path)
	}
	if err := w.Remove(ctx, false); err != nil {
		return "The isolated worktree could not be cleaned up: " + err.Error()
	}
	return "The sub-agent made no changes; its worktree was removed."
}

// repoRoot returns the top of the git repository containing dir, or "".
func repoRoot(dir string) string {
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sanitize(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == ' ' || r == '_' || r == '/':
			return '-'
		}
		return -1
	}, s)
	out = strings.Trim(out, "-")
	if out == "" {
		return "sub"
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// ensure context stays imported even if a build tag trims a caller.
var _ = context.Background
