package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/autopilot"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/worktree"
)

// cmdAutopilot manages and runs the autopilot work queue.
func cmdAutopilot(args []string) error {
	if len(args) == 0 {
		return autopilotUsage()
	}
	ctx := context.Background()
	store := autopilot.NewStore(config.Path("autopilot"))

	switch strings.ToLower(args[0]) {
	case "add", "new":
		if len(args) < 3 {
			return fmt.Errorf(`usage: antares autopilot add "title" "the task prompt"`)
		}
		c, err := store.Add(args[1], strings.Join(args[2:], " "))
		if err != nil {
			return err
		}
		fmt.Printf("Added %s: %s\n", c.ID, c.Title)
		return nil

	case "list", "ls":
		cards := store.List()
		if len(cards) == 0 {
			fmt.Println(`No cards. Add one with: antares autopilot add "title" "prompt"`)
			return nil
		}
		for _, c := range cards {
			pr := ""
			if c.PR != "" {
				pr = "  " + c.PR
			}
			fmt.Printf("%s  [%-8s]  %s%s\n", c.ID, c.Status, c.Title, pr)
			if c.Error != "" {
				fmt.Printf("    error: %s\n", c.Error)
			}
		}
		return nil

	case "run":
		wantPR := false
		for _, a := range args[1:] {
			if a == "--pr" {
				wantPR = true
			}
		}
		return autopilotRun(ctx, store, wantPR)

	default:
		return autopilotUsage()
	}
}

func autopilotUsage() error {
	fmt.Println(`Run a queue of tasks unattended:
  antares autopilot add "title" "the task prompt"
  antares autopilot list
  antares autopilot run [--pr]      # process pending cards; --pr opens PRs`)
	return nil
}

func autopilotRun(ctx context.Context, store *autopilot.Store, wantPR bool) error {
	pending := store.Pending()
	if len(pending) == 0 {
		fmt.Println("No pending cards.")
		return nil
	}
	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	workspace := config.Expand(rt.cfg.Agent.Workspace)
	verifyCmd := strings.TrimSpace(rt.cfg.Autopilot.VerifyCommand)
	base := firstNonBlank(rt.cfg.Autopilot.BaseBranch, "main")

	runner := &autopilot.Runner{
		Workspace: workspace,
		Work: func(ctx context.Context, prompt, ws string) (string, error) {
			res, err := rt.agent.Run(ctx, agent.Request{
				Message:   prompt,
				Role:      "coder",
				Workspace: ws,
				Platform:  "autopilot",
				SystemExtra: "You are running unattended in the autopilot. Complete the task fully, " +
					"make the edits, and leave the workspace in a working state. Nobody can answer questions.",
			}, nil)
			if err != nil {
				return "", err
			}
			return res.Reply, nil
		},
		Isolate: worktreeIsolation(ctx, workspace),
	}
	if verifyCmd != "" {
		runner.Verify = func(ctx context.Context, ws string) (string, bool) {
			out, code := runShell(ctx, ws, verifyCmd)
			return out, code == 0
		}
	}
	if wantPR {
		runner.Publish = func(ctx context.Context, c autopilot.Card, ws string) (string, error) {
			return openPR(ctx, ws, base, c.Title, c.Result)
		}
	}

	for _, c := range pending {
		fmt.Printf("→ %s: %s\n", c.ID, c.Title)
		done := runner.Process(ctx, store, c)
		fmt.Printf("  %s\n", done.Status)
		if done.Error != "" {
			fmt.Printf("  %s\n", done.Error)
		}
		if done.PR != "" {
			fmt.Printf("  %s\n", done.PR)
		}
	}
	return nil
}

// worktreeIsolation gives each card its own git worktree when the workspace is a
// repository; otherwise cards share the workspace.
func worktreeIsolation(_ context.Context, workspace string) autopilot.Isolation {
	if !worktree.Available(workspace) {
		return nil
	}
	return func(ctx context.Context, label string) (string, func(bool), func(), error) {
		wt, err := worktree.Create(ctx, workspace, label)
		if err != nil {
			return "", nil, nil, err
		}
		keptDirty := false
		keep := func(dirty bool) { keptDirty = dirty && wt.Dirty(ctx) }
		cleanup := func() {
			if !keptDirty {
				_ = wt.Remove(ctx, true)
			}
		}
		return wt.Path, keep, cleanup, nil
	}
}

func runShell(ctx context.Context, dir, command string) (string, int) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	return string(out), code
}

// openPR commits the worktree, pushes its branch, and opens a PR with gh.
func openPR(ctx context.Context, ws, base, title, body string) (string, error) {
	branch := "autopilot/" + worktreeBranch(title)
	steps := [][]string{
		{"git", "checkout", "-B", branch},
		{"git", "add", "-A"},
		{"git", "commit", "-m", title},
		{"git", "push", "-u", "origin", branch},
	}
	for _, s := range steps {
		if out, code := runArgs(ctx, ws, s); code != 0 {
			return "", fmt.Errorf("%s failed: %s", strings.Join(s, " "), strings.TrimSpace(out))
		}
	}
	out, code := runArgs(ctx, ws, []string{"gh", "pr", "create", "--base", base, "--head", branch, "--title", title, "--body", body})
	if code != 0 {
		return "", fmt.Errorf("gh pr create failed: %s", strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

func runArgs(ctx context.Context, dir string, args []string) (string, int) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	return string(out), code
}

func worktreeBranch(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "task"
	}
	return s
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
