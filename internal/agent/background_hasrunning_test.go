package agent

import (
	"testing"

	"github.com/enowdev/antares/internal/tools"
)

func TestBGManagerHasRunning(t *testing.T) {
	m := newBGManager()
	m.tasks["t1"] = &bgTask{parentSession: "sess-A", info: tools.TaskInfo{Status: "running"}}
	m.tasks["t2"] = &bgTask{parentSession: "sess-A", info: tools.TaskInfo{Status: "done"}}
	m.tasks["t3"] = &bgTask{parentSession: "sess-B", info: tools.TaskInfo{Status: "running"}}

	if !m.hasRunning("sess-A") {
		t.Error("sess-A has a running task (t1) — hasRunning should be true")
	}
	if !m.hasRunning("sess-B") {
		t.Error("sess-B has a running task (t3) — hasRunning should be true")
	}
	if m.hasRunning("sess-C") {
		t.Error("sess-C has no tasks — hasRunning should be false")
	}
	if m.hasRunning("") {
		t.Error("empty parent must never report running")
	}

	// Once sess-A's only running task finishes, it must report false — this is
	// the transition that lets the coordinator's next (woken) turn resume normal
	// todo-nudging instead of ending silently.
	m.tasks["t1"].info.Status = "done"
	if m.hasRunning("sess-A") {
		t.Error("sess-A's task finished — hasRunning should be false")
	}
}
