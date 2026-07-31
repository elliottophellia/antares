package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
)

func TestPrepareSubAgentWorkspaceInheritsParentProject(t *testing.T) {
	project := t.TempDir()
	a := &Agent{cfg: &config.Config{Agent: config.Agent{Workspace: "/global/antares-workspace"}}}

	workspace, projectDir, wt := a.prepareSubAgentWorkspace(
		context.Background(),
		Request{ProjectDir: project, Workspace: project},
		tools.SubAgentRequest{},
	)

	if workspace != project {
		t.Fatalf("workspace = %q, want inherited project %q", workspace, project)
	}
	if projectDir != project {
		t.Fatalf("projectDir = %q, want %q", projectDir, project)
	}
	if wt != nil {
		t.Fatal("non-isolated worker unexpectedly created a worktree")
	}
}

func TestPrepareSubAgentWorkspaceIsolationFailureKeepsParentProject(t *testing.T) {
	project := t.TempDir() // deliberately not a git repository
	a := &Agent{cfg: &config.Config{Agent: config.Agent{Workspace: "/global/antares-workspace"}}}

	workspace, projectDir, wt := a.prepareSubAgentWorkspace(
		context.Background(),
		Request{ProjectDir: project, Workspace: project},
		tools.SubAgentRequest{Isolate: true, Role: "coder"},
	)

	if workspace != project || projectDir != project || wt != nil {
		t.Fatalf("fallback = (%q, %q, %v), want inherited project without worktree", workspace, projectDir, wt)
	}
}

func TestPrepareSubAgentWorkspaceIsolatesParentProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	project := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", project}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-q", "-m", "initial")

	a := &Agent{cfg: &config.Config{Agent: config.Agent{Workspace: "/global/antares-workspace"}}}
	workspace, projectDir, wt := a.prepareSubAgentWorkspace(
		context.Background(),
		Request{ProjectDir: project, Workspace: project},
		tools.SubAgentRequest{Isolate: true, Role: "coder"},
	)
	if wt == nil {
		t.Fatal("isolation did not create a worktree from the parent project")
	}
	t.Cleanup(func() { wt.Cleanup(context.Background()) })
	if workspace != wt.Path {
		t.Fatalf("workspace = %q, want worktree %q", workspace, wt.Path)
	}
	if projectDir != "" {
		t.Fatalf("isolated projectDir = %q, want empty", projectDir)
	}
	if _, err := os.Stat(filepath.Join(workspace, "README.md")); err != nil {
		t.Fatalf("worktree does not contain parent project: %v", err)
	}
}
