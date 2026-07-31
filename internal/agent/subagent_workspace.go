package agent

import (
	"context"
	"log/slog"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
	"github.com/enowdev/antares/internal/worktree"
)

// prepareSubAgentWorkspace gives synchronous and background delegation identical
// workspace semantics. A worker inherits its parent's project by default. When
// isolation is requested, the worktree is created from that project (not from
// the global Antares workspace); failure falls back to the inherited project so
// the worker can still read and edit the files it was delegated.
func (a *Agent) prepareSubAgentWorkspace(ctx context.Context, parent Request, sub tools.SubAgentRequest) (workspace, projectDir string, wt *worktree.Worktree) {
	workspace = strings.TrimSpace(sub.Workspace)
	parentProject := strings.TrimSpace(parent.ProjectDir)

	if parentProject != "" {
		projectDir = parentProject
		if workspace == "" {
			workspace = parentProject
		}
	} else if workspace == "" {
		workspace = firstNonEmpty(parent.Workspace, a.cfg.Agent.Workspace)
	}

	if !sub.Isolate {
		return workspace, projectDir, nil
	}

	base := firstNonEmpty(sub.Workspace, parentProject, parent.Workspace, a.cfg.Agent.Workspace)
	w, err := worktree.Create(ctx, config.Expand(base), firstNonEmpty(sub.Role, "sub"))
	if err != nil {
		slog.Warn("worktree isolation unavailable; using inherited workspace", "workspace", base, "error", err)
		return workspace, projectDir, nil
	}
	return w.Path, "", w
}
