package server

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/autopilot"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/worktree"
)

// autopilotRunning guards against two run requests processing the same queue at
// once.
var autopilotRunning sync.Mutex

func (s *Server) autopilotStore() *autopilot.Store {
	return autopilot.NewStore(config.Path("autopilot"))
}

func (s *Server) handleAutopilotList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"cards": s.autopilotStore().List()})
}

func (s *Server) handleAutopilotAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	c, err := s.autopilotStore().Add(strings.TrimSpace(body.Title), strings.TrimSpace(body.Prompt))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleAutopilotRemove(w http.ResponseWriter, r *http.Request) {
	store := s.autopilotStore()
	c, ok := store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	// Removing is a soft delete: mark it and let it drop from the pending list.
	// (The store has no hard delete; a failed/verified card simply stays as
	// history, which is what the journal is for.)
	c.Status = autopilot.Failed
	c.Error = "removed"
	_ = store.Update(c)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAutopilotRun kicks off processing of the pending cards in the background
// and returns at once; the UI polls the list for status.
func (s *Server) handleAutopilotRun(w http.ResponseWriter, r *http.Request) {
	store := s.autopilotStore()
	if len(store.Pending()) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"started": 0})
		return
	}
	if !autopilotRunning.TryLock() {
		writeJSON(w, http.StatusOK, map[string]any{"started": 0, "already_running": true})
		return
	}

	cfg := s.config()
	runner := s.autopilotRunner(cfg)
	pending := len(store.Pending())
	go func() {
		defer autopilotRunning.Unlock()
		runner.RunAll(context.Background(), store)
	}()
	writeJSON(w, http.StatusOK, map[string]any{"started": pending})
}

// autopilotRunner wires the pipeline to the live agent, a per-card worktree, and
// the configured verify command.
func (s *Server) autopilotRunner(cfg *config.Config) *autopilot.Runner {
	workspace := config.Expand(cfg.Agent.Workspace)
	verifyCmd := strings.TrimSpace(cfg.Autopilot.VerifyCommand)

	runner := &autopilot.Runner{
		Workspace: workspace,
		Work: func(ctx context.Context, prompt, ws string) (string, error) {
			res, err := s.agent.Run(ctx, agent.Request{
				Message: prompt, Role: "coder", Workspace: ws, Platform: "autopilot",
				SystemExtra: "You are running unattended in the autopilot. Complete the task fully and " +
					"leave the workspace in a working state. Nobody can answer questions.",
			}, nil)
			if err != nil {
				return "", err
			}
			return res.Reply, nil
		},
	}
	if worktree.Available(workspace) {
		runner.Isolate = func(ctx context.Context, label string) (string, func(bool), func(), error) {
			wt, err := worktree.Create(ctx, workspace, label)
			if err != nil {
				return "", nil, nil, err
			}
			kept := false
			keep := func(dirty bool) { kept = dirty && wt.Dirty(ctx) }
			cleanup := func() {
				if !kept {
					_ = wt.Remove(ctx, true)
				}
			}
			return wt.Path, keep, cleanup, nil
		}
	}
	if verifyCmd != "" {
		runner.Verify = func(ctx context.Context, ws string) (string, bool) {
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", verifyCmd)
			cmd.Dir = ws
			out, err := cmd.CombinedOutput()
			return string(out), err == nil
		}
	}
	return runner
}
