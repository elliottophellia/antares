// Package autopilot runs queued work items to completion with as little
// supervision as the task allows: each card is taken into its own isolated
// workspace, worked by the agent, verified, and recorded — so a backlog can be
// ground through unattended and every outcome is auditable afterwards.
package autopilot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is where a card sits in the pipeline.
type Status string

const (
	Pending  Status = "pending"
	Running  Status = "running"
	Verified Status = "verified" // worked and passed verification
	Failed   Status = "failed"   // errored or failed verification
	Merged   Status = "merged"   // a PR was opened/merged
)

// Card is one unit of work.
type Card struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Prompt       string    `json:"prompt"`
	Status       Status    `json:"status"`
	Result       string    `json:"result,omitempty"`        // the agent's final reply
	VerifyOutput string    `json:"verify_output,omitempty"` // the verification command's output
	Branch       string    `json:"branch,omitempty"`        // the worktree branch, if kept
	PR           string    `json:"pr,omitempty"`            // the PR url, if opened
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Store persists cards as one JSON file each.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore opens (creating if needed) a card store under dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// Add stores a new card and returns it with its id and timestamps set.
func (s *Store) Add(title, prompt string) (Card, error) {
	if strings.TrimSpace(title) == "" {
		return Card{}, fmt.Errorf("a card needs a title")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	c := Card{
		ID: newID(now), Title: title, Prompt: prompt, Status: Pending,
		CreatedAt: now, UpdatedAt: now,
	}
	return c, s.save(c)
}

// Get returns one card.
func (s *Store) Get(id string) (Card, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(id)
}

// Update writes a card back, stamping UpdatedAt.
func (s *Store) Update(c Card) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.UpdatedAt = time.Now()
	return s.save(c)
}

// List returns every card, newest first.
func (s *Store) List() []Card {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Card
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if c, ok := s.load(strings.TrimSuffix(e.Name(), ".json")); ok {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Pending returns the cards still waiting to run, oldest first.
func (s *Store) Pending() []Card {
	all := s.List()
	var out []Card
	for i := len(all) - 1; i >= 0; i-- { // oldest first
		if all[i].Status == Pending {
			out = append(out, all[i])
		}
	}
	return out
}

func (s *Store) save(c Card) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(c.ID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(c.ID))
}

func (s *Store) load(id string) (Card, bool) {
	raw, err := os.ReadFile(s.path(id))
	if err != nil {
		return Card{}, false
	}
	var c Card
	if json.Unmarshal(raw, &c) != nil {
		return Card{}, false
	}
	return c, true
}

func newID(now time.Time) string {
	return fmt.Sprintf("card_%d", now.UnixNano())
}

// ---- runner -----------------------------------------------------------------

// Isolation makes an isolated workspace for a card and returns the path plus a
// cleanup. When it returns "", the shared workspace is used.
type Isolation func(ctx context.Context, label string) (path string, keep func(dirty bool), cleanup func(), err error)

// Runner drives one card through the pipeline. Its collaborators are injected so
// the orchestration is testable without a real agent, git, or CI.
type Runner struct {
	// Work runs the agent on a prompt in a workspace and returns its reply.
	Work func(ctx context.Context, prompt, workspace string) (string, error)
	// Verify runs the verification (build/test/lint) in a workspace, returning
	// its output and whether it passed. Nil means "always verified".
	Verify func(ctx context.Context, workspace string) (output string, ok bool)
	// Isolate provides a per-card workspace. Nil runs in Workspace directly.
	Isolate Isolation
	// Workspace is the fallback when Isolate is nil.
	Workspace string
	// Publish opens a PR for a verified, dirty worktree. Nil skips publishing.
	Publish func(ctx context.Context, c Card, workspace string) (prURL string, err error)
}

// Process takes one card from pending to a terminal state, updating the store as
// it goes.
func (r *Runner) Process(ctx context.Context, store *Store, c Card) Card {
	c.Status = Running
	c.UpdatedAt = time.Now()
	_ = store.Update(c)

	workspace := r.Workspace
	var keep func(bool)
	var cleanup func()
	if r.Isolate != nil {
		path, k, cl, err := r.Isolate(ctx, c.Title)
		if err == nil && path != "" {
			workspace, keep, cleanup = path, k, cl
		}
	}

	reply, err := r.Work(ctx, c.Prompt, workspace)
	if err != nil {
		c.Status = Failed
		c.Error = err.Error()
		return r.finish(ctx, store, c, workspace, keep, cleanup)
	}
	c.Result = reply

	if r.Verify != nil {
		out, ok := r.Verify(ctx, workspace)
		c.VerifyOutput = out
		if !ok {
			c.Status = Failed
			c.Error = "verification failed"
			return r.finish(ctx, store, c, workspace, keep, cleanup)
		}
	}
	c.Status = Verified

	if r.Publish != nil {
		if pr, err := r.Publish(ctx, c, workspace); err == nil && pr != "" {
			c.PR = pr
			c.Status = Merged
		} else if err != nil {
			c.Error = "verified but could not open a PR: " + err.Error()
		}
	}
	return r.finish(ctx, store, c, workspace, keep, cleanup)
}

func (r *Runner) finish(_ context.Context, store *Store, c Card, workspace string, keep func(bool), cleanup func()) Card {
	// Keep the worktree when work landed (verified/merged); otherwise let it be
	// cleaned up. keep(dirty) tells the isolation whether to preserve it.
	if keep != nil {
		keep(c.Status == Verified || c.Status == Merged)
	}
	if cleanup != nil {
		cleanup()
	}
	_ = store.Update(c)
	return c
}

// RunAll processes every pending card in order and returns the finished cards.
func (r *Runner) RunAll(ctx context.Context, store *Store) []Card {
	var done []Card
	for _, c := range store.Pending() {
		if ctx.Err() != nil {
			break
		}
		done = append(done, r.Process(ctx, store, c))
	}
	return done
}
