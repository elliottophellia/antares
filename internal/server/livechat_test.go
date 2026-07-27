package server

import (
	"context"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/agent"
)

func TestLiveRun_ReplayThenFollow(t *testing.T) {
	lr := newLiveRun()
	lr.publish(agent.Event{Type: agent.EventText, Delta: "a"})
	lr.publish(agent.Event{Type: agent.EventText, Delta: "b"})

	// A follower joining late must first replay the backlog, then see new events.
	got := make(chan string, 8)
	go func() {
		_ = lr.follow(context.Background(), 0, func(e agent.Event) error {
			got <- e.Delta
			return nil
		})
		close(got)
	}()

	// Backlog.
	if v := <-got; v != "a" {
		t.Fatalf("want a, got %q", v)
	}
	if v := <-got; v != "b" {
		t.Fatalf("want b, got %q", v)
	}
	// Live.
	lr.publish(agent.Event{Type: agent.EventText, Delta: "c"})
	if v := <-got; v != "c" {
		t.Fatalf("want c, got %q", v)
	}
	// Finish closes the follower.
	lr.finish()
	select {
	case _, ok := <-got:
		if ok {
			// drain any trailing value then expect close
			<-got
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not return after finish")
	}
}

func TestLiveRun_FollowFromCursor(t *testing.T) {
	lr := newLiveRun()
	lr.publish(agent.Event{Type: agent.EventText, Delta: "x"})
	lr.publish(agent.Event{Type: agent.EventText, Delta: "y"})
	lr.finish()

	var seen []string
	_ = lr.follow(context.Background(), 1, func(e agent.Event) error {
		seen = append(seen, e.Delta)
		return nil
	})
	if len(seen) != 1 || seen[0] != "y" {
		t.Fatalf("cursor replay wrong: %v", seen)
	}
}

func TestLiveRun_FollowStopsOnContextCancel(t *testing.T) {
	lr := newLiveRun()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- lr.follow(ctx, 0, func(agent.Event) error { return nil })
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context error")
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not stop on cancel")
	}
}

func TestLiveHub_PutGetRemove(t *testing.T) {
	h := newLiveHub()
	lr := newLiveRun()
	h.put("s1", lr)
	if h.get("s1") != lr {
		t.Fatal("get did not return the run")
	}
	// A stale remove (different run) must not evict the current one.
	h.remove("s1", newLiveRun())
	if h.get("s1") != lr {
		t.Fatal("stale remove evicted the live run")
	}
	h.remove("s1", lr)
	if h.get("s1") != nil {
		t.Fatal("run not removed")
	}
}
