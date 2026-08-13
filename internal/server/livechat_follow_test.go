package server

import (
	"context"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/agent"
)

// A client whose socket buffer is full stalls inside send with the run's lock
// dropped, while the turn keeps publishing — a build streaming through terminal
// emits one tool_progress event per chunk, and those are not coalesced — so the
// window trims past the stalled cursor. follow must survive that and pick up at
// the oldest event still retained.
func TestFollowSurvivesWindowTrimWhileSending(t *testing.T) {
	lr := newLiveRun()
	// Seed one event so the follower has a backlog to send at once: the handshake
	// below needs it inside send, not parked waiting for the first publish.
	lr.publish(agent.Event{Type: agent.EventToolProgress, Chunk: "seed"})

	type followResult struct {
		panicked any
		err      error
		cursors  []int
	}
	result := make(chan followResult, 1)
	entered := make(chan struct{})
	release := make(chan struct{})

	go func() {
		var res followResult
		defer func() {
			res.panicked = recover()
			result <- res
		}()
		stalled := false
		res.err = lr.follow(context.Background(), 0, func(_ agent.Event, cursor int) error {
			res.cursors = append(res.cursors, cursor)
			if !stalled {
				stalled = true
				close(entered)
				<-release
			}
			return nil
		})
	}()

	// The follower is now inside send with the lock released; overrun the window
	// from under it.
	<-entered
	const overrun = 200
	for i := 0; i < maxLiveEvents+overrun; i++ {
		lr.publish(agent.Event{Type: agent.EventToolProgress, Chunk: "x"})
	}
	close(release)
	lr.finish()

	var res followResult
	select {
	case res = <-result:
	case <-time.After(30 * time.Second):
		t.Fatal("follow did not return")
	}
	if res.panicked != nil {
		t.Fatalf("follow panicked: %v", res.panicked)
	}
	if res.err != nil {
		t.Fatalf("follow: %v", res.err)
	}

	// One seed plus maxLiveEvents+overrun published, so the oldest retained event
	// sits at absolute index overrun+1 and send reports the cursor just past it.
	if len(res.cursors) < 2 {
		t.Fatalf("follower saw %d events, want the seed plus the retained window", len(res.cursors))
	}
	if got, want := res.cursors[1], overrun+2; got != want {
		t.Fatalf("resumed at cursor %d, want %d (oldest retained event)", got, want)
	}
	if got, want := len(res.cursors), 1+maxLiveEvents; got != want {
		t.Fatalf("follower saw %d events, want %d", got, want)
	}
}
