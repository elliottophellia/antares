package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/enowdev/antares/internal/tools"
)

// insert adds a running task directly, standing in for startBackground without
// needing a live model.
func insert(m *bgManager, id, role, task string) {
	_, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.tasks[id] = &bgTask{
		info:   tools.TaskInfo{ID: id, Role: role, Task: task, Status: "running"},
		cancel: cancel,
	}
	m.mu.Unlock()
}

func TestBackgroundFinishSuccess(t *testing.T) {
	m := newBGManager()
	insert(m, "task_1", "coder", "do a thing")
	m.finish("task_1", &Result{Reply: "all done"}, nil, nil)

	got, ok := m.status("task_1")
	if !ok || got.Status != "done" || got.Output != "all done" {
		t.Fatalf("expected done with output, got %+v", got)
	}
	if got.EndedAt.IsZero() {
		t.Fatal("EndedAt should be set on finish")
	}
}

func TestBackgroundFinishError(t *testing.T) {
	m := newBGManager()
	insert(m, "task_2", "", "fails")
	m.finish("task_2", nil, errors.New("boom"), nil)
	got, _ := m.status("task_2")
	if got.Status != "error" || got.Error != "boom" {
		t.Fatalf("expected error status, got %+v", got)
	}
}

func TestBackgroundStopMarksStopped(t *testing.T) {
	m := newBGManager()
	insert(m, "task_3", "", "long job")
	if !m.stop("task_3") {
		t.Fatal("stop should report success for a running task")
	}
	got, _ := m.status("task_3")
	if got.Status != "stopped" {
		t.Fatalf("expected stopped, got %s", got.Status)
	}
	// A subsequent finish with a cancelled context must not overwrite "stopped".
	m.finish("task_3", nil, context.Canceled, context.Canceled)
	got, _ = m.status("task_3")
	if got.Status != "stopped" {
		t.Fatalf("finish overwrote stopped, got %s", got.Status)
	}
}

func TestBackgroundStopUnknown(t *testing.T) {
	m := newBGManager()
	if m.stop("nope") {
		t.Fatal("stopping an unknown task should report false")
	}
}

func TestBackgroundListNewestFirst(t *testing.T) {
	m := newBGManager()
	insert(m, "a", "", "first")
	insert(m, "b", "", "second")
	// Give them distinct start times.
	m.tasks["a"].info.StartedAt = m.tasks["a"].info.StartedAt.Add(-1)
	list := m.list("")
	if len(list) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(list))
	}
}

func TestContinueRunningTaskRejected(t *testing.T) {
	a := &Agent{bg: newBGManager()}
	insert(a.bg, "task_run", "", "busy")
	if _, err := a.continueTask("task_run", "hi"); err == nil {
		t.Fatal("continuing a running task should error")
	}
}

func TestContinueUnknownTask(t *testing.T) {
	a := &Agent{bg: newBGManager()}
	if _, err := a.continueTask("nope", "hi"); err == nil {
		t.Fatal("continuing an unknown task should error")
	}
}

func TestContinueFinishedWithoutSession(t *testing.T) {
	a := &Agent{bg: newBGManager()}
	insert(a.bg, "task_done", "", "done")
	a.bg.finish("task_done", &Result{Reply: "x"}, nil, nil) // no SessionID
	if _, err := a.continueTask("task_done", "more"); err == nil {
		t.Fatal("a task with no session cannot be continued")
	}
}
