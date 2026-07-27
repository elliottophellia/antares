package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/tools"
)

// A background task is a sub-agent that runs detached from the turn that
// started it. Synchronous delegation blocks the parent until the worker is
// done, which is fine for one quick lookup but wrong for several long
// workstreams: the parent should fire them off, keep working, and collect the
// results when they are ready. That is what these tasks are for.

type bgTask struct {
	info   tools.TaskInfo
	cancel context.CancelFunc
}

// bgManager holds every background task for the process, keyed by id.
type bgManager struct {
	mu    sync.Mutex
	tasks map[string]*bgTask
}

func newBGManager() *bgManager { return &bgManager{tasks: map[string]*bgTask{}} }

// backgroundFor returns a BackgroundTasks bound to the run that spawns the
// work, so a task inherits the parent's depth and identity.
func (a *Agent) backgroundFor(parent Request) tools.BackgroundTasks {
	return &bgHook{a: a, parent: parent}
}

type bgHook struct {
	a      *Agent
	parent Request
}

func (h *bgHook) Start(req tools.SubAgentRequest) string {
	return h.a.startBackground(h.parent, req)
}
func (h *bgHook) Status(id string) (tools.TaskInfo, bool) { return h.a.bg.status(id) }
func (h *bgHook) List(parent string) []tools.TaskInfo     { return h.a.bg.list(parent) }
func (h *bgHook) Stop(id string) bool                     { return h.a.bg.stop(id) }

// startBackground launches a sub-agent in its own goroutine and returns its id
// immediately. The task keeps running after the parent turn ends.
func (a *Agent) startBackground(parent Request, req tools.SubAgentRequest) string {
	id := newID("task")
	// Detached from the turn context — the parent turn returns before this
	// finishes — but cancellable via Stop.
	ctx, cancel := context.WithCancel(context.Background())

	task := &bgTask{
		info: tools.TaskInfo{
			ID: id, Role: req.Role, Task: truncTask(req.Prompt),
			Status: "running", StartedAt: time.Now(),
		},
		cancel: cancel,
	}
	a.bg.mu.Lock()
	a.bg.tasks[id] = task
	a.bg.mu.Unlock()

	depth := parent.Depth + 1
	untrack := trackSubAgent(req.Role, req.Prompt, parent.SessionID)

	go func() {
		defer cancel()
		defer untrack()

		maxTurns := req.MaxTurns
		if maxTurns <= 0 {
			maxTurns = 20
		}
		workspace := firstNonEmpty(req.Workspace, a.cfg.Agent.Workspace)

		res, err := a.Run(ctx, Request{
			Message:     req.Prompt,
			SystemExtra: req.SystemExtra,
			Toolset:     req.Toolset,
			Model:       req.Model,
			Role:        req.Role,
			Workspace:   workspace,
			MaxTurns:    maxTurns,
			Platform:    "background",
			UserID:      parent.UserID,
			Quiet:       true,
			Depth:       depth,
		}, nil)

		a.bg.finish(id, res, err, ctx.Err())
	}()

	return id
}

// finish records a task's outcome. A cancelled context means Stop was called.
func (m *bgManager) finish(id string, res *Result, err, ctxErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return
	}
	t.info.EndedAt = time.Now()
	switch {
	case ctxErr != nil && t.info.Status == "stopped":
		// left as stopped
	case err != nil:
		t.info.Status = "error"
		t.info.Error = err.Error()
	default:
		t.info.Status = "done"
		if res != nil {
			t.info.Output = res.Reply
		}
	}
}

func (m *bgManager) status(id string) (tools.TaskInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return tools.TaskInfo{}, false
	}
	return t.info, true
}

func (m *bgManager) list(parent string) []tools.TaskInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]tools.TaskInfo, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.info)
	}
	// Newest first.
	sortTasksByStart(out)
	return out
}

func (m *bgManager) stop(id string) bool {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if ok && t.info.Status == "running" {
		t.info.Status = "stopped"
		t.info.EndedAt = time.Now()
		m.mu.Unlock()
		t.cancel()
		return true
	}
	m.mu.Unlock()
	return ok
}

// BackgroundTasks lists every task for the swarm/status views.
func (a *Agent) BackgroundTasks() []tools.TaskInfo { return a.bg.list("") }

func truncTask(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 100 {
		return s[:100] + "…"
	}
	return s
}

func sortTasksByStart(list []tools.TaskInfo) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].StartedAt.After(list[j-1].StartedAt); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
