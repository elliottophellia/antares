package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/store"
)

// Executor runs one scheduled job's prompt and returns its final reply.
type Executor func(ctx context.Context, job store.CronJob) (sessionID, reply string, err error)

// Deliverer optionally forwards a job's output to a messaging target.
type Deliverer func(ctx context.Context, target, text string) error

// Runner ticks once a minute and executes jobs whose schedule has come due.
type Runner struct {
	db       store.Store
	exec     Executor
	deliver  Deliverer
	location *time.Location

	maxConcurrent int
	historyLimit  int

	mu      sync.Mutex
	running map[string]context.CancelFunc
	sem     chan struct{}
}

// Options configures a Runner.
type Options struct {
	Store         store.Store
	Execute       Executor
	Deliver       Deliverer
	Timezone      string
	MaxConcurrent int
	HistoryLimit  int
}

// New builds a Runner.
func New(o Options) *Runner {
	loc := time.Local
	if tz := strings.TrimSpace(o.Timezone); tz != "" && !strings.EqualFold(tz, "local") {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		} else {
			slog.Warn("unknown cron timezone; falling back to local", "timezone", tz, "error", err)
		}
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 3
	}
	if o.HistoryLimit <= 0 {
		o.HistoryLimit = 200
	}
	return &Runner{
		db: o.Store, exec: o.Execute, deliver: o.Deliver, location: loc,
		maxConcurrent: o.MaxConcurrent, historyLimit: o.HistoryLimit,
		running: map[string]context.CancelFunc{},
		sem:     make(chan struct{}, o.MaxConcurrent),
	}
}

// Start runs the scheduler loop until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
	// Refresh next_run for every job so the dashboard is accurate immediately.
	if err := r.recomputeAll(ctx); err != nil {
		slog.Warn("cron: initial schedule computation failed", "error", err)
	}

	// Align the first tick to the next whole minute so jobs fire on time.
	now := time.Now()
	first := now.Truncate(time.Minute).Add(time.Minute).Sub(now)
	timer := time.NewTimer(first)
	defer timer.Stop()

	slog.Info("cron scheduler started", "timezone", r.location.String())

	for {
		select {
		case <-ctx.Done():
			slog.Info("cron scheduler stopped")
			return
		case <-timer.C:
			r.tick(ctx, time.Now().In(r.location))
			next := time.Now()
			timer.Reset(next.Truncate(time.Minute).Add(time.Minute).Sub(next))
		}
	}
}

// tick launches every job that is due at t.
func (r *Runner) tick(ctx context.Context, t time.Time) {
	jobs, err := r.db.ListCronJobs(ctx)
	if err != nil {
		slog.Error("cron: cannot list jobs", "error", err)
		return
	}
	for _, job := range jobs {
		if !job.Enabled || job.NextRun == nil {
			continue
		}
		if job.NextRun.After(t) {
			continue
		}
		r.launch(ctx, job)
	}
}

// launch executes one job, bounded by the concurrency limit.
func (r *Runner) launch(ctx context.Context, job store.CronJob) {
	r.mu.Lock()
	if _, busy := r.running[job.ID]; busy {
		r.mu.Unlock()
		slog.Warn("cron: previous run still active, skipping", "job", job.Name)
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.running[job.ID] = cancel
	r.mu.Unlock()

	// Advance next_run before executing so a long run cannot double-fire.
	r.advance(ctx, &job, time.Now().In(r.location))

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, job.ID)
			r.mu.Unlock()
			cancel()
		}()

		select {
		case r.sem <- struct{}{}:
			defer func() { <-r.sem }()
		case <-runCtx.Done():
			return
		}
		r.execute(runCtx, job)
	}()
}

// RunNow executes a job immediately, outside its schedule.
func (r *Runner) RunNow(ctx context.Context, id string) error {
	job, err := r.db.GetCronJob(ctx, id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	_, busy := r.running[id]
	r.mu.Unlock()
	if busy {
		return fmt.Errorf("job %q is already running", job.Name)
	}
	go r.execute(context.WithoutCancel(ctx), *job)
	return nil
}

// execute performs the run and records its outcome.
func (r *Runner) execute(ctx context.Context, job store.CronJob) {
	run := &store.CronRun{
		ID: newID("run"), JobID: job.ID, Status: "running", StartedAt: time.Now(),
	}
	if err := r.db.PutCronRun(ctx, run); err != nil {
		slog.Warn("cron: cannot record run start", "error", err)
	}
	slog.Info("cron job started", "job", job.Name, "schedule", job.Schedule)

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	sessionID, reply, err := r.exec(runCtx, job)
	finished := time.Now()
	run.FinishedAt = &finished
	run.SessionID = sessionID
	run.Output = truncate(reply, 20000)

	state := "ok"
	if err != nil {
		state = "error"
		run.Status = "error"
		run.Error = err.Error()
		slog.Error("cron job failed", "job", job.Name, "error", err)
	} else {
		run.Status = "ok"
		slog.Info("cron job finished", "job", job.Name, "seconds", finished.Sub(run.StartedAt).Seconds())

		if target := strings.TrimSpace(job.Target); target != "" && r.deliver != nil && reply != "" {
			if derr := r.deliver(runCtx, target, reply); derr != nil {
				slog.Warn("cron: delivery failed", "job", job.Name, "target", target, "error", derr)
				run.Error = "delivery failed: " + derr.Error()
			}
		}
	}

	if err := r.db.PutCronRun(ctx, run); err != nil {
		slog.Warn("cron: cannot record run result", "error", err)
	}

	now := time.Now()
	job.LastRun = &now
	job.LastState = state
	if err := r.db.PutCronJob(ctx, &job); err != nil {
		slog.Warn("cron: cannot update job state", "error", err)
	}
}

// advance recomputes and persists a job's next activation.
func (r *Runner) advance(ctx context.Context, job *store.CronJob, from time.Time) {
	sched, err := r.scheduleFor(*job)
	if err != nil {
		slog.Warn("cron: invalid schedule, disabling job", "job", job.Name, "error", err)
		job.Enabled = false
		job.LastState = "invalid schedule"
		job.NextRun = nil
	} else {
		next := sched.Next(from)
		if next.IsZero() {
			job.NextRun = nil
		} else {
			job.NextRun = &next
		}
	}
	if err := r.db.PutCronJob(ctx, job); err != nil {
		slog.Warn("cron: cannot persist next run", "error", err)
	}
}

// Recompute refreshes one job's next activation, e.g. after an edit.
func (r *Runner) Recompute(ctx context.Context, job *store.CronJob) error {
	sched, err := r.scheduleFor(*job)
	if err != nil {
		return err
	}
	next := sched.Next(time.Now().In(r.location))
	if next.IsZero() {
		job.NextRun = nil
	} else {
		job.NextRun = &next
	}
	return r.db.PutCronJob(ctx, job)
}

func (r *Runner) recomputeAll(ctx context.Context) error {
	jobs, err := r.db.ListCronJobs(ctx)
	if err != nil {
		return err
	}
	for i := range jobs {
		if !jobs[i].Enabled {
			continue
		}
		if err := r.Recompute(ctx, &jobs[i]); err != nil {
			slog.Warn("cron: cannot compute next run", "job", jobs[i].Name, "error", err)
		}
	}
	return nil
}

// scheduleFor parses a job's expression in the job's own timezone when set.
func (r *Runner) scheduleFor(job store.CronJob) (*Schedule, error) {
	return Parse(job.Schedule)
}

// Location reports the scheduler timezone.
func (r *Runner) Location() *time.Location { return r.location }

// Validate checks an expression and returns the next activation for previews.
func Validate(expr string, loc *time.Location) (time.Time, error) {
	s, err := Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	if loc == nil {
		loc = time.Local
	}
	return s.Next(time.Now().In(loc)), nil
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
