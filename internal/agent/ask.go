package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/tools"
)

// The ask desk pauses a turn while ask_user waits for the person to answer.
// Unlike ending the turn and treating the next chat message as the reply, this
// blocks the tool call itself: the run holds, no further output is produced,
// and the answer comes back as the tool's result so the SAME turn continues.
// It mirrors the approval desk, the other place a tool blocks on a human.

// AskQuestion is one question put to the user.
type AskQuestion struct {
	Question    string   `json:"question"`
	Header      string   `json:"header,omitempty"`
	Options     []string `json:"options,omitempty"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
}

// pendingAsk is a question set waiting for an answer.
type pendingAsk struct {
	ID        string        `json:"id"`
	SessionID string        `json:"session_id"`
	Questions []AskQuestion `json:"questions"`
	CreatedAt time.Time     `json:"created_at"`

	answered chan string
}

type askDesk struct {
	mu      sync.Mutex
	pending map[string]*pendingAsk
}

var asks = askDesk{pending: map[string]*pendingAsk{}}

// PendingAsks lists the questions currently waiting, so a client that connects
// mid-pause (a reload) can render them.
func (a *Agent) PendingAsks() []pendingAsk {
	asks.mu.Lock()
	defer asks.mu.Unlock()
	out := make([]pendingAsk, 0, len(asks.pending))
	for _, r := range asks.pending {
		out = append(out, pendingAsk{ID: r.ID, SessionID: r.SessionID, Questions: r.Questions, CreatedAt: r.CreatedAt})
	}
	return out
}

// ResolveAsk delivers the person's answer to a waiting question set. It reports
// false when the id is unknown (already answered, or the run was cancelled).
func (a *Agent) ResolveAsk(id, answer string) bool {
	asks.mu.Lock()
	r, ok := asks.pending[id]
	if ok {
		delete(asks.pending, id)
	}
	asks.mu.Unlock()
	if !ok {
		return false
	}
	r.answered <- answer // buffered, so this never blocks
	return true
}

// askUser registers a question set, tells whatever is watching to render it,
// and blocks until the answer arrives or the run is cancelled (stop / socket
// closed). There is deliberately no timeout: an interactive question waits as
// long as the person needs. A cancelled context unblocks it so a stopped run
// never leaks a goroutine.
func (a *Agent) askUser(ctx context.Context, sessionID string, qs []AskQuestion, emit Emit) (string, error) {
	req := &pendingAsk{
		ID:        newID("ask"),
		SessionID: sessionID,
		Questions: qs,
		CreatedAt: time.Now(),
		answered:  make(chan string, 1),
	}
	asks.mu.Lock()
	asks.pending[req.ID] = req
	asks.mu.Unlock()
	defer func() {
		asks.mu.Lock()
		delete(asks.pending, req.ID)
		asks.mu.Unlock()
	}()

	payload, _ := json.Marshal(map[string]any{"id": req.ID, "questions": qs})
	if emit != nil {
		_ = emit(Event{Type: EventAsk, ID: req.ID, Name: "ask_user", Content: string(payload)})
	}

	select {
	case ans := <-req.answered:
		return ans, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// askBridge adapts askUser to the tools.AskFunc signature, so the ask_user tool
// can block without importing this package.
func (a *Agent) askBridge(sessionID string, emit Emit) tools.AskFunc {
	return func(ctx context.Context, qs []tools.AskQuestion) (string, error) {
		conv := make([]AskQuestion, len(qs))
		for i, q := range qs {
			conv[i] = AskQuestion{Question: q.Question, Header: q.Header, Options: q.Options, MultiSelect: q.MultiSelect}
		}
		return a.askUser(ctx, sessionID, conv, emit)
	}
}
