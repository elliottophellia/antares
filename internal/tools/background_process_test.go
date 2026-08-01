package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func waitProcessDone(t *testing.T, job *backgroundProcess, timeout time.Duration) {
	t.Helper()
	select {
	case <-job.done:
	case <-time.After(timeout):
		t.Fatalf("process %s did not finish within %s", job.id, timeout)
	}
}

func TestBackgroundProcessIncrementalOutputAndCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	job, err := m.startBackground("session-a", t.TempDir(), "printf first; sleep 0.15; printf second", 0)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	first := job.view(0, true)
	if first.Status != processRunning || first.Output != "first" {
		t.Fatalf("first poll = %#v, want running with first", first)
	}
	waitProcessDone(t, job, 2*time.Second)
	final := job.view(0, true)
	if final.Status != processCompleted || final.Output != "second" {
		t.Fatalf("final poll = %#v, want completed with second", final)
	}
	if final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", final.ExitCode)
	}
	if again := job.view(0, true); again.Output != "" || again.NextOffset != final.NextOffset {
		t.Fatalf("consumed output was returned twice: %#v", again)
	}
}

func TestBackgroundProcessLogOffsetDoesNotConsumePollCursor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	job, err := m.startBackground("session-a", t.TempDir(), "printf abcdef", 0)
	if err != nil {
		t.Fatal(err)
	}
	waitProcessDone(t, job, 2*time.Second)
	logView := job.view(3, false)
	if logView.Output != "def" || logView.NextOffset != 6 {
		t.Fatalf("offset log = %#v", logView)
	}
	poll := job.view(0, true)
	if poll.Output != "abcdef" {
		t.Fatalf("log read consumed poll cursor: %q", poll.Output)
	}
}

func TestConcurrentPollsConsumeOutputOnce(t *testing.T) {
	job := &backgroundProcess{id: "proc_test", status: processCompleted, startedAt: time.Now()}
	_, _ = job.log.Write([]byte("once"))
	start := make(chan struct{})
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- job.view(0, true).Output
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a+b != "once" {
		t.Fatalf("concurrent poll output = %q + %q, want exactly one copy", a, b)
	}
}

func TestBackgroundProcessTimeoutUsesRealLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	job, err := m.startBackground("session-a", t.TempDir(), "printf started; sleep 30", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	waitProcessDone(t, job, 3*time.Second)
	got := job.view(0, false)
	if got.Status != processTimedOut {
		t.Fatalf("status = %s, want %s", got.Status, processTimedOut)
	}
	if !strings.Contains(got.Output, "started") {
		t.Fatalf("partial output lost: %q", got.Output)
	}
}

func TestBackgroundProcessReportsNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	job, err := m.startBackground("session-a", t.TempDir(), "printf failed; exit 7", 0)
	if err != nil {
		t.Fatal(err)
	}
	waitProcessDone(t, job, 2*time.Second)
	got := job.view(0, false)
	if got.Status != processFailed || got.ExitCode == nil || *got.ExitCode != 7 || got.Output != "failed" {
		t.Fatalf("failed process result = %#v", got)
	}
}

func TestBackgroundProcessKillTerminatesProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	stoppedFile := filepath.Join(dir, "child.stopped")
	job, err := m.startBackground("session-a", dir, "sh -c 'trap \"echo stopped > child.stopped; exit 0\" TERM; echo $$ > child.pid; while :; do sleep 1; done' & wait", 0)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child pid file was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	job.stopAndWait(processCancelled)
	waitProcessDone(t, job, time.Second)
	if got := job.view(0, false).Status; got != processCancelled {
		t.Fatalf("status = %s, want cancelled", got)
	}
	deadline = time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(stoppedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child process did not receive the process-group termination signal")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProcessLogPaginationAndRetention(t *testing.T) {
	var log processLog
	payload := strings.Repeat("x", backgroundReadLimit+123)
	if _, err := log.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	first, next, truncated, more := log.read(0)
	if len(first) != backgroundReadLimit || next != backgroundReadLimit || truncated || !more {
		t.Fatalf("first page = len %d, next %d, truncated %v, more %v", len(first), next, truncated, more)
	}
	second, end, truncated, more := log.read(next)
	if len(second) != 123 || end != int64(len(payload)) || truncated || more {
		t.Fatalf("second page = len %d, end %d, truncated %v, more %v", len(second), end, truncated, more)
	}

	oversize := strings.Repeat("y", backgroundLogLimit+10)
	if _, err := log.Write([]byte(oversize)); err != nil {
		t.Fatal(err)
	}
	_, _, truncated, _ = log.read(0)
	if !truncated {
		t.Fatal("reader behind the retention window was not told output was truncated")
	}
}

func TestBackgroundProcessHandlesAreSessionScoped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	job, err := m.startBackground("owner", t.TempDir(), "true", 0)
	if err != nil {
		t.Fatal(err)
	}
	waitProcessDone(t, job, 2*time.Second)
	if _, ok := m.getJob("other", job.id); ok {
		t.Fatal("another session accessed a background process handle")
	}
	if _, ok := m.getJob("owner", job.id); !ok {
		t.Fatal("owner could not access its process handle")
	}
}

func TestProcessToolPollAndWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	job, err := m.startBackground("session-a", t.TempDir(), "printf tool-ok", 0)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"action": "wait", "process_id": job.id, "timeout": 2})
	result := (processTool{}).Execute(context.Background(), Input{
		Args: args, SessionID: "session-a", Deps: &Deps{Shell: m}, Emit: func(Progress) {},
	})
	if result.IsError {
		t.Fatalf("process tool failed: %s", result.Content)
	}
	var got processView
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != processCompleted || got.Output != "tool-ok" {
		t.Fatalf("process result = %#v", got)
	}
}

func TestProcessWaitReturnsRunningWhenBoundExpires(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	job, err := m.startBackground("session-a", t.TempDir(), "sleep 30", 0)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"action": "wait", "process_id": job.id, "timeout": 1})
	started := time.Now()
	result := (processTool{}).Execute(context.Background(), Input{
		Args: args, SessionID: "session-a", Deps: &Deps{Shell: m}, Emit: func(Progress) {},
	})
	if result.IsError {
		t.Fatal(result.Content)
	}
	var got processView
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if got.Status != processRunning || elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("bounded wait returned after %s with status %s", elapsed, got.Status)
	}
}

func TestTerminalToolStartsManagedBackgroundProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	cfg := &config.Config{Terminal: config.Terminal{Sandbox: "none"}}
	m := NewShellManager(cfg.Terminal)
	t.Cleanup(m.CloseAll)
	args, _ := json.Marshal(map[string]any{"command": "printf managed", "background": true})
	result := (terminalTool{}).Execute(context.Background(), Input{
		Args: args, SessionID: "session-a", Workspace: t.TempDir(),
		Deps: &Deps{Shell: m, Config: cfg}, Emit: func(Progress) {},
	})
	if result.IsError {
		t.Fatalf("terminal background start failed: %s", result.Content)
	}
	var started processView
	if err := json.Unmarshal([]byte(result.Content), &started); err != nil {
		t.Fatal(err)
	}
	if started.ProcessID == "" || (started.Status != processRunning && started.Status != processCompleted) {
		t.Fatalf("start result = %#v", started)
	}
	job, ok := m.getJob("session-a", started.ProcessID)
	if !ok {
		t.Fatal("returned process handle is not registered")
	}
	waitProcessDone(t, job, 2*time.Second)
}

func TestProcessToolIsExposedWithTerminalToolsets(t *testing.T) {
	for _, set := range []string{"default", "coding", "security", "reverse", "vibecoder"} {
		found := false
		for _, name := range ExpandToolset(set) {
			if name == "process" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("toolset %q exposes terminal but not process", set)
		}
	}
}

func TestRegistryAddsProcessCompanionForExplicitTerminal(t *testing.T) {
	r := NewRegistry()
	r.Register(terminalTool{})
	r.Register(processTool{})
	resolved := r.Resolve("minimal", []string{"terminal"}, nil)
	seen := map[string]bool{}
	for _, tool := range resolved {
		seen[tool.Name()] = true
	}
	if !seen["terminal"] || !seen["process"] {
		t.Fatalf("explicit terminal resolved to tools %v", seen)
	}

	resolved = r.Resolve("default", nil, []string{"process"})
	for _, tool := range resolved {
		if tool.Name() == "process" {
			t.Fatal("explicit process disable was ignored")
		}
	}
}

func TestCancelRunningStopsOnlyOwnedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	m := NewShellManager(config.Terminal{Sandbox: "none"})
	t.Cleanup(m.CloseAll)
	owned, err := m.startBackground("owner", t.TempDir(), "sleep 30", 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.startBackground("other", t.TempDir(), "sleep 30", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.CancelRunning("owner"); got != 1 {
		t.Fatalf("CancelRunning stopped %d processes, want 1", got)
	}
	waitProcessDone(t, owned, time.Second)
	if got := owned.view(0, false).Status; got != processCancelled {
		t.Fatalf("owned status = %s", got)
	}
	if got := other.view(0, false).Status; got != processRunning {
		t.Fatalf("other session status = %s, want running", got)
	}
}
