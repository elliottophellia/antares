package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/sandbox"
)

const (
	backgroundLogLimit  = 2 << 20 // retain the newest 2 MiB per process
	backgroundReadLimit = 60_000  // keep one tool result inside the normal context budget
	backgroundJobLimit  = 32
	completedJobTTL     = time.Hour
)

type processStatus string

const (
	processRunning   processStatus = "running"
	processCompleted processStatus = "completed"
	processFailed    processStatus = "failed"
	processCancelled processStatus = "cancelled"
	processTimedOut  processStatus = "timed_out"
)

type processLog struct {
	mu   sync.Mutex
	data []byte
	base int64
}

func (l *processLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = append(l.data, p...)
	if drop := len(l.data) - backgroundLogLimit; drop > 0 {
		copy(l.data, l.data[drop:])
		l.data = l.data[:len(l.data)-drop]
		l.base += int64(drop)
	}
	return len(p), nil
}

func (l *processLog) read(offset int64) (chunk string, next int64, truncated, hasMore bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if offset < l.base {
		offset = l.base
		truncated = true
	}
	end := l.base + int64(len(l.data))
	if offset > end {
		offset = end
	}
	next = offset + min(int64(backgroundReadLimit), end-offset)
	return string(l.data[offset-l.base : next-l.base]), next, truncated, next < end
}

type backgroundProcess struct {
	mu        sync.Mutex
	id        string
	sessionID string
	command   string
	workspace string
	cmd       *exec.Cmd
	log       processLog
	readAt    int64
	status    processStatus
	exitCode  *int
	startedAt time.Time
	endedAt   time.Time
	done      chan struct{}
	stopOnce  sync.Once
}

type processView struct {
	ProcessID  string        `json:"process_id"`
	Status     processStatus `json:"status"`
	Command    string        `json:"command,omitempty"`
	Workspace  string        `json:"workspace,omitempty"`
	PID        int           `json:"pid,omitempty"`
	ExitCode   *int          `json:"exit_code,omitempty"`
	StartedAt  time.Time     `json:"started_at"`
	EndedAt    *time.Time    `json:"ended_at,omitempty"`
	Output     string        `json:"output,omitempty"`
	NextOffset int64         `json:"next_offset"`
	Truncated  bool          `json:"truncated,omitempty"`
	HasMore    bool          `json:"has_more,omitempty"`
}

func newProcessID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "proc_" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("proc_%x", time.Now().UnixNano())
}

func (m *ShellManager) startBackground(sessionID, workspace, command string, timeout time.Duration) (*backgroundProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapJobsLocked(time.Now())
	active := 0
	for _, job := range m.jobs {
		job.mu.Lock()
		if job.sessionID == sessionID && job.status == processRunning {
			active++
		}
		job.mu.Unlock()
	}
	if active >= backgroundJobLimit {
		return nil, fmt.Errorf("session already has %d running background processes", active)
	}

	cmd, err := m.backgroundCommand(sessionID, workspace, command)
	if err != nil {
		return nil, err
	}
	job := &backgroundProcess{
		id:        newProcessID(),
		sessionID: sessionID,
		command:   command,
		workspace: workspace,
		cmd:       cmd,
		status:    processRunning,
		startedAt: time.Now().UTC(),
		done:      make(chan struct{}),
	}
	cmd.Stdout = &job.log
	cmd.Stderr = &job.log
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start background process: %w", err)
	}

	m.jobs[job.id] = job

	go job.wait()
	if timeout > 0 {
		go func() {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case <-timer.C:
				job.stop(processTimedOut)
			case <-job.done:
			}
		}()
	}
	return job, nil
}

func (m *ShellManager) backgroundCommand(sessionID, workspace, command string) (*exec.Cmd, error) {
	shell, _ := defaultShell(m.cfg.Shell)
	switch strings.ToLower(m.cfg.Backend) {
	case "docker":
		image := m.cfg.DockerImage
		if image == "" {
			image = "debian:bookworm-slim"
		}
		net := "none"
		if m.cfg.AllowNetwork {
			net = "bridge"
		}
		return exec.Command("docker", "run", "--rm", "-i", "--network", net,
			"-v", workspace+":/workspace", "-w", "/workspace", image, "/bin/sh", "-c", command), nil
	case "ssh":
		if m.cfg.SSHHost == "" {
			return nil, fmt.Errorf("terminal.ssh_host is not configured")
		}
		return exec.Command("ssh", "-T", m.cfg.SSHHost, "/bin/sh -c "+shellQuote(command)), nil
	default:
		mode, note := sandbox.Resolve(sandbox.Mode(m.cfg.Sandbox))
		if note != "" {
			m.warnSandboxOnce(note)
		}
		policy := sandbox.Policy{Workspace: workspace, AllowNetwork: m.cfg.AllowNetwork, Hidden: m.hiddenPaths()}
		cmd, err := sandbox.Command(mode, policy, shell, "-c", command)
		if err != nil {
			m.warnSandboxOnce(err.Error())
			cmd = exec.Command(shell, "-c", command)
		}
		cmd.Dir = workspace
		env := append(os.Environ(), "ANTARES_SESSION="+sessionID, "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat")
		if m.httpShim.dir != "" {
			env = withShimEnv(env, m.httpShim)
		}
		cmd.Env = env
		return cmd, nil
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func (j *backgroundProcess) wait() {
	err := j.cmd.Wait()
	j.mu.Lock()
	if j.status == processRunning {
		code := j.cmd.ProcessState.ExitCode()
		j.exitCode = &code
		if err == nil && code == 0 {
			j.status = processCompleted
		} else {
			j.status = processFailed
		}
	}
	j.endedAt = time.Now().UTC()
	j.mu.Unlock()
	j.stopOnce.Do(func() { close(j.done) })
}

func (j *backgroundProcess) stop(status processStatus) {
	j.mu.Lock()
	if j.status != processRunning {
		j.mu.Unlock()
		return
	}
	j.status = status
	cmd := j.cmd
	j.mu.Unlock()
	terminateProcessGroup(cmd)
	go func() {
		select {
		case <-j.done:
		case <-time.After(2 * time.Second):
			killProcessGroup(cmd)
		}
	}()
}

func (j *backgroundProcess) stopAndWait(status processStatus) {
	stopProcesses([]*backgroundProcess{j}, status)
}

func stopProcesses(jobs []*backgroundProcess, status processStatus) {
	for _, job := range jobs {
		job.stop(status)
	}
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-time.After(3 * time.Second):
			killProcessGroup(job.cmd)
			select {
			case <-job.done:
			case <-time.After(time.Second):
			}
		}
	}
}

func (j *backgroundProcess) view(offset int64, consume bool) processView {
	j.mu.Lock()
	defer j.mu.Unlock()
	if consume {
		offset = j.readAt
	}
	view := processView{ProcessID: j.id, Status: j.status, Command: j.command, Workspace: j.workspace,
		ExitCode: j.exitCode, StartedAt: j.startedAt}
	if j.cmd != nil && j.cmd.Process != nil {
		view.PID = j.cmd.Process.Pid
	}
	if !j.endedAt.IsZero() {
		ended := j.endedAt
		view.EndedAt = &ended
	}
	view.Output, view.NextOffset, view.Truncated, view.HasMore = j.log.read(offset)
	if consume {
		j.readAt = view.NextOffset
	}
	return view
}

func (m *ShellManager) getJob(sessionID, id string) (*backgroundProcess, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok || job.sessionID != sessionID {
		return nil, false
	}
	return job, true
}

func (m *ShellManager) listJobs(sessionID string) []processView {
	m.mu.Lock()
	m.reapJobsLocked(time.Now())
	var jobs []*backgroundProcess
	for _, job := range m.jobs {
		if job.sessionID == sessionID {
			jobs = append(jobs, job)
		}
	}
	m.mu.Unlock()
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].startedAt.Before(jobs[k].startedAt) })
	out := make([]processView, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, job.view(0, false))
		out[len(out)-1].Output = ""
	}
	return out
}

// CancelRunning stops every managed process owned by a session. It is used by
// the agent interrupt path so the dashboard Stop button reaches backend process
// groups instead of only cancelling the current model/tool context.
func (m *ShellManager) CancelRunning(sessionID string) int {
	m.mu.Lock()
	var jobs []*backgroundProcess
	for _, job := range m.jobs {
		job.mu.Lock()
		running := job.sessionID == sessionID && job.status == processRunning
		job.mu.Unlock()
		if running {
			jobs = append(jobs, job)
		}
	}
	m.mu.Unlock()
	stopProcesses(jobs, processCancelled)
	return len(jobs)
}

func (m *ShellManager) reapJobsLocked(now time.Time) {
	for id, job := range m.jobs {
		job.mu.Lock()
		stale := job.status != processRunning && !job.endedAt.IsZero() && now.Sub(job.endedAt) > completedJobTTL
		job.mu.Unlock()
		if stale {
			delete(m.jobs, id)
		}
	}
}

type processTool struct{}

func (processTool) Name() string { return "process" }
func (processTool) Description() string {
	return "Inspect and control background terminal processes by handle. Prefer wait or poll over shell sleep when completion time is unknown."
}
func (processTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":     propEnum("Operation: list session processes, poll status and consume new output, read log at an offset, wait for a state change/completion, or kill the process group.", "list", "poll", "log", "wait", "kill"),
		"process_id": prop("string", "Handle returned by terminal(background=true). Required except for list."),
		"offset":     propDefault("integer", "Absolute log byte offset for action=log.", 0),
		"timeout":    propDefault("integer", "Maximum seconds to block for action=wait (1-30). This is a wait bound, not an assumed job duration.", 10),
	}, "action")
}

func (processTool) Execute(ctx context.Context, in Input) Result {
	if in.Deps == nil || in.Deps.Shell == nil {
		return Errorf("terminal backend is not available")
	}
	var args struct {
		Action    string `json:"action"`
		ProcessID string `json:"process_id"`
		Offset    int64  `json:"offset"`
		Timeout   int    `json:"timeout"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.Action == "list" {
		return processJSON(in.Deps.Shell.listJobs(in.SessionID))
	}
	job, ok := in.Deps.Shell.getJob(in.SessionID, args.ProcessID)
	if !ok {
		return Errorf("background process %q not found in this session", args.ProcessID)
	}
	switch args.Action {
	case "poll":
		return processJSON(job.view(0, true))
	case "log":
		if args.Offset < 0 {
			return Errorf("offset must be non-negative")
		}
		return processJSON(job.view(args.Offset, false))
	case "wait":
		if args.Timeout <= 0 {
			args.Timeout = 10
		}
		if args.Timeout > 30 {
			args.Timeout = 30
		}
		select {
		case <-job.done:
		case <-time.After(time.Duration(args.Timeout) * time.Second):
		case <-ctx.Done():
		}
		return processJSON(job.view(0, true))
	case "kill":
		job.stop(processCancelled)
		select {
		case <-job.done:
		case <-time.After(3 * time.Second):
		}
		return processJSON(job.view(0, true))
	default:
		return Errorf("unknown action %q", args.Action)
	}
}

func processJSON(v any) Result {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Errorf("encode process state: %v", err)
	}
	return Result{Content: string(b)}
}
