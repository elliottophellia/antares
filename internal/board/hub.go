package board

import "sync"

// hub is a per-session pub/sub so the dashboard can stream board changes in
// realtime instead of polling. The todo tool and the board tool publish a
// session key whenever they change that session's board; a board-stream handler
// subscribes and pushes a fresh board to the browser on every signal.
type hub struct {
	mu   sync.Mutex
	seq  int
	subs map[int]subscriber
}

type subscriber struct {
	key string // the session this subscriber cares about
	ch  chan struct{}
}

var changes = &hub{subs: map[int]subscriber{}}

// Notify signals that a session's board changed. Non-blocking: a slow or full
// subscriber simply misses this tick and catches up on the next one, since each
// tick just tells the handler to re-read and re-send the whole board.
func Notify(key string) {
	if key == "" {
		key = "default"
	}
	changes.mu.Lock()
	defer changes.mu.Unlock()
	for _, s := range changes.subs {
		if s.key != key {
			continue
		}
		select {
		case s.ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe returns a channel that receives a signal whenever the given
// session's board changes, plus a cancel func to unsubscribe.
func Subscribe(key string) (<-chan struct{}, func()) {
	if key == "" {
		key = "default"
	}
	ch := make(chan struct{}, 1)
	changes.mu.Lock()
	changes.seq++
	id := changes.seq
	changes.subs[id] = subscriber{key: key, ch: ch}
	changes.mu.Unlock()
	return ch, func() {
		changes.mu.Lock()
		if s, ok := changes.subs[id]; ok {
			delete(changes.subs, id)
			close(s.ch)
		}
		changes.mu.Unlock()
	}
}

