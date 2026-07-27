package agent

import (
	"context"
	"fmt"
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
	info      tools.TaskInfo
	cancel    context.CancelFunc
	sessionID string // the sub-agent's session, so it can be continued
	depth     int
	userID    string
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
func (h *bgHook) Send(id, message string) (string, error) { return h.a.continueTask(id, message) }

// startBackground launches a sub-agent in its own goroutine and returns its id
// immediately. The task keeps running after the parent turn ends.
func (a *Agent) startBackground(parent Request, req tools.SubAgentRequest) string {
	id := newID("task")
	// Detached from the turn context — the parent turn returns before this
	// finishes — but cancellable via Stop.
	ctx, cancel := context.WithCancel(context.Background())

	depth := parent.Depth + 1
	task := &bgTask{
		info: tools.TaskInfo{
			ID: id, Role: req.Role, Task: truncTask(req.Prompt),
			Status: "running", StartedAt: time.Now(),
		},
		cancel: cancel,
		depth:  depth,
		userID: parent.UserID,
	}
	a.bg.mu.Lock()
	a.bg.tasks[id] = task
	a.bg.mu.Unlock()

	untrack := trackSubAgent(req.Role, req.Prompt, parent.SessionID)

	go func() {
		defer cancel()
		defer untrack()

		maxTurns := req.MaxTurns
		if maxTurns <= 0 {
			maxTurns = 20
		}
		workspace := firstNonEmpty(req.Workspace, a.cfg.Agent.Workspace)

		// Not Quiet: the sub-agent keeps a real session, so the task can be
		// continued later with the task tool's send action.
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
			Depth:       depth,
		}, nil)

		a.bg.finish(id, res, err, ctx.Err())
	}()

	return id
}

// continueTask sends a follow-up to a finished task, running another turn on the
// same sub-session so the coordinator can iterate with a worker. It runs
// synchronously and returns the new reply.
func (a *Agent) continueTask(id, message string) (string, error) {
	a.bg.mu.Lock()
	t, ok := a.bg.tasks[id]
	if !ok {
		a.bg.mu.Unlock()
		return "", fmt.Errorf("no task %q", id)
	}
	if t.info.Status == "running" {
		a.bg.mu.Unlock()
		return "", fmt.Errorf("task %s is still running — wait for it before sending a follow-up", id)
	}
	if t.sessionID == "" {
		a.bg.mu.Unlock()
		return "", fmt.Errorf("task %s has no session to continue", id)
	}
	sid, depth, uid := t.sessionID, t.depth, t.userID
	t.info.Status = "running"
	a.bg.mu.Unlock()

	res, err := a.Run(context.Background(), Request{
		SessionID: sid,
		Message:   message,
		Platform:  "background",
		UserID:    uid,
		Depth:     depth,
	}, nil)

	a.bg.mu.Lock()
	defer a.bg.mu.Unlock()
	t.info.EndedAt = time.Now()
	if err != nil {
		t.info.Status = "error"
		t.info.Error = err.Error()
		return "", err
	}
	t.info.Status = "done"
	if res != nil {
		t.info.Output = res.Reply
		return res.Reply, nil
	}
	return "", nil
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
	if res != nil && res.SessionID != "" {
		t.sessionID = res.SessionID
	}
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
