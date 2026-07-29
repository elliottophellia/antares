package agent

import (
	"context"
	"sync"
)

// A sub-agent runs its own full turn loop, and until now its event stream was
// thrown away — only coarse "sub-agent: <tool>" progress leaked to the parent.
// SubStream keeps each sub-agent's events in an append-only log that any number
// of dashboard clients can replay and then follow live, exactly like the main
// turn's liveRun. That is what lets the UI list the running sub-agents and, on
// click, watch one's streaming text — then flip back to the main agent.

// SubStream is the live event log for one sub-agent.
type SubStream struct {
	mu      sync.Mutex
	events  []Event
	done    bool
	updated chan struct{} // closed on every change; replaced under the lock
}

func newSubStream() *SubStream { return &SubStream{updated: make(chan struct{})} }

func (ss *SubStream) signal() {
	close(ss.updated)
	ss.updated = make(chan struct{})
}

func (ss *SubStream) publish(e Event) {
	ss.mu.Lock()
	ss.events = append(ss.events, e)
	ss.signal()
	ss.mu.Unlock()
}

func (ss *SubStream) finish() {
	ss.mu.Lock()
	if !ss.done {
		ss.done = true
		ss.signal()
	}
	ss.mu.Unlock()
}

// Follow replays events from cursor, then blocks for new ones until the
// sub-agent finishes or ctx is cancelled. send stops the follow by erroring.
func (ss *SubStream) Follow(ctx context.Context, cursor int, send func(Event) error) error {
	i := cursor
	for {
		ss.mu.Lock()
		for i < len(ss.events) {
			e := ss.events[i]
			i++
			ss.mu.Unlock()
			if err := send(e); err != nil {
				return err
			}
			ss.mu.Lock()
		}
		if ss.done {
			ss.mu.Unlock()
			return nil
		}
		wait := ss.updated
		ss.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

// subStreams is the process-wide registry of sub-agent streams, keyed by the
// sub-agent's id (the same id used by trackSubAgent and background tasks, so a
// client listing the active agents can attach to any of them). Finished streams
// linger briefly-less: they are removed once the sub-agent ends AND its buffer
// has been marked done, so a client attaching right at the end still replays it.
var subStreams = struct {
	mu      sync.Mutex
	streams map[string]*SubStream
}{streams: map[string]*SubStream{}}

// openSubStream creates (or returns) the stream for a sub-agent id.
func openSubStream(id string) *SubStream {
	subStreams.mu.Lock()
	defer subStreams.mu.Unlock()
	if ss, ok := subStreams.streams[id]; ok {
		return ss
	}
	ss := newSubStream()
	subStreams.streams[id] = ss
	return ss
}

// closeSubStream finishes a sub-agent's stream and drops it from the registry.
// Followers already attached still drain to completion; new attaches get a
// short-lived "done" via GetSubStream returning nil.
func closeSubStream(id string) {
	subStreams.mu.Lock()
	ss := subStreams.streams[id]
	delete(subStreams.streams, id)
	subStreams.mu.Unlock()
	if ss != nil {
		ss.finish()
	}
}

// GetSubStream returns the live stream for a sub-agent id, or nil if none is
// running (or it already finished). Exported for the server's attach handler.
func GetSubStream(id string) *SubStream {
	subStreams.mu.Lock()
	defer subStreams.mu.Unlock()
	return subStreams.streams[id]
}

// swarmBus signals whenever the set of active sub-agents changes (one starts or
// ends), so the dashboard's sub-agent list updates in realtime instead of
// polling. Same non-blocking fan-out as the board hub.
var swarmBus = struct {
	mu   sync.Mutex
	seq  int
	subs map[int]chan struct{}
}{subs: map[int]chan struct{}{}}

func notifySwarm() {
	swarmBus.mu.Lock()
	defer swarmBus.mu.Unlock()
	for _, ch := range swarmBus.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// SubscribeSwarm returns a channel that fires when the active sub-agent set
// changes, plus a cancel func. Exported for the server's swarm-stream handler.
func SubscribeSwarm() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	swarmBus.mu.Lock()
	swarmBus.seq++
	id := swarmBus.seq
	swarmBus.subs[id] = ch
	swarmBus.mu.Unlock()
	return ch, func() {
		swarmBus.mu.Lock()
		if c, ok := swarmBus.subs[id]; ok {
			delete(swarmBus.subs, id)
			close(c)
		}
		swarmBus.mu.Unlock()
	}
}

// subEmit wraps a sub-agent's run so every event is mirrored onto its stream,
// while still invoking any inner emit (progress forwarding to the parent).
func subEmit(id string, inner func(Event) error) func(Event) error {
	ss := openSubStream(id)
	return func(e Event) error {
		ss.publish(e)
		if inner != nil {
			return inner(e)
		}
		return nil
	}
}

