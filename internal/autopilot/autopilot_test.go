package autopilot

import (
	"context"
	"errors"
	"testing"
)

func TestStoreAddListPending(t *testing.T) {
	s := NewStore(t.TempDir())
	c, err := s.Add("first", "do the thing")
	if err != nil || c.Status != Pending {
		t.Fatalf("add failed: %+v %v", c, err)
	}
	if len(s.List()) != 1 || len(s.Pending()) != 1 {
		t.Fatal("card not listed as pending")
	}
	if _, err := s.Add("", "no title"); err == nil {
		t.Fatal("a card without a title should error")
	}
}

func TestProcessVerifiedKeepsWorktree(t *testing.T) {
	s := NewStore(t.TempDir())
	c, _ := s.Add("task", "prompt")

	kept := false
	cleaned := false
	r := &Runner{
		Work:   func(ctx context.Context, p, ws string) (string, error) { return "done", nil },
		Verify: func(ctx context.Context, ws string) (string, bool) { return "build ok", true },
		Isolate: func(ctx context.Context, label string) (string, func(bool), func(), error) {
			return "/tmp/wt", func(dirty bool) { kept = dirty }, func() { cleaned = true }, nil
		},
	}
	done := r.Process(context.Background(), s, c)
	if done.Status != Verified || done.Result != "done" || done.VerifyOutput != "build ok" {
		t.Fatalf("unexpected outcome: %+v", done)
	}
	if !kept || !cleaned {
		t.Fatalf("worktree lifecycle wrong: kept=%v cleaned=%v", kept, cleaned)
	}
	// Persisted.
	got, _ := s.Get(c.ID)
	if got.Status != Verified {
		t.Fatalf("not persisted: %s", got.Status)
	}
}

func TestProcessFailedVerification(t *testing.T) {
	s := NewStore(t.TempDir())
	c, _ := s.Add("task", "prompt")
	r := &Runner{
		Work:   func(ctx context.Context, p, ws string) (string, error) { return "done", nil },
		Verify: func(ctx context.Context, ws string) (string, bool) { return "FAIL: tests", false },
	}
	done := r.Process(context.Background(), s, c)
	if done.Status != Failed || done.Error == "" {
		t.Fatalf("expected failure, got %+v", done)
	}
}

func TestProcessWorkError(t *testing.T) {
	s := NewStore(t.TempDir())
	c, _ := s.Add("task", "prompt")
	r := &Runner{Work: func(ctx context.Context, p, ws string) (string, error) { return "", errors.New("boom") }}
	done := r.Process(context.Background(), s, c)
	if done.Status != Failed || done.Error != "boom" {
		t.Fatalf("expected work error, got %+v", done)
	}
}

func TestProcessPublishesPR(t *testing.T) {
	s := NewStore(t.TempDir())
	c, _ := s.Add("task", "prompt")
	r := &Runner{
		Work:    func(ctx context.Context, p, ws string) (string, error) { return "done", nil },
		Publish: func(ctx context.Context, c Card, ws string) (string, error) { return "https://pr/1", nil },
	}
	done := r.Process(context.Background(), s, c)
	if done.Status != Merged || done.PR != "https://pr/1" {
		t.Fatalf("expected merged with PR, got %+v", done)
	}
}

func TestRunAllProcessesPending(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Add("a", "1")
	_, _ = s.Add("b", "2")
	r := &Runner{Work: func(ctx context.Context, p, ws string) (string, error) { return "ok", nil }}
	done := r.RunAll(context.Background(), s)
	if len(done) != 2 {
		t.Fatalf("expected 2 processed, got %d", len(done))
	}
	if len(s.Pending()) != 0 {
		t.Fatal("cards should no longer be pending")
	}
}
