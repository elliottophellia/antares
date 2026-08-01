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
		_ = lr.follow(context.Background(), 0, func(e agent.Event, _ int) error {
			got <- e.Delta
			return nil
		})
		close(got)
	}()

	// Adjacent backlog deltas are coalesced into one frame.
	if v := <-got; v != "ab" {
		t.Fatalf("want ab, got %q", v)
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

func TestLiveRun_CoalescesBacklogAndReportsAbsoluteCursor(t *testing.T) {
	lr := newLiveRun()
	for i := 0; i < 4000; i++ {
		lr.publish(agent.Event{Type: agent.EventReasoning, Delta: "x"})
	}
	lr.publish(agent.Event{Type: agent.EventUsage, InputTokens: 10})
	lr.finish()

	var frames []agent.Event
	var cursors []int
	if err := lr.follow(context.Background(), 0, func(e agent.Event, cursor int) error {
		frames = append(frames, e)
		cursors = append(cursors, cursor)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("4001 backlog events produced %d frames, want 2", len(frames))
	}
	if got := len(frames[0].Delta); got != 3999 {
		t.Fatalf("coalesced retained reasoning length = %d, want 3999", got)
	}
	if cursors[0] != 4000 || cursors[1] != 4001 {
		t.Fatalf("absolute cursors = %v, want [4000 4001]", cursors)
	}

	// Reattaching at the reported cursor must not replay the reasoning backlog.
	var replayed []agent.Event
	if err := lr.follow(context.Background(), cursors[0], func(e agent.Event, _ int) error {
		replayed = append(replayed, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0].Type != agent.EventUsage {
		t.Fatalf("cursor replay = %#v, want only usage event", replayed)
	}
}

func TestLiveRun_FollowFromCursor(t *testing.T) {
	lr := newLiveRun()
	lr.publish(agent.Event{Type: agent.EventText, Delta: "x"})
	lr.publish(agent.Event{Type: agent.EventText, Delta: "y"})
	lr.finish()

	var seen []string
	_ = lr.follow(context.Background(), 1, func(e agent.Event, _ int) error {
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
		done <- lr.follow(ctx, 0, func(agent.Event, int) error { return nil })
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
