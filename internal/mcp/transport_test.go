package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// replyOnWrite stands in for the child's stdin. Writing a request frame to it
// answers that frame on the transport's stdout straight away and then pauses,
// which is the ordering a fast local server produces: the reply is on the wire
// before the caller that wrote the request has run another statement.
type replyOnWrite struct {
	out    *os.File
	settle time.Duration
	err    error // returned instead of writing, when set
}

func (w *replyOnWrite) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	var req struct {
		ID *int64 `json:"id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(p), &req); err == nil && req.ID != nil {
		frame, _ := json.Marshal(rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		})
		if _, err := w.out.Write(append(frame, '\n')); err != nil {
			return 0, err
		}
		// Give the background reader time to pick the reply up and look its id
		// up in the pending map, so the ordering under test is not a race.
		time.Sleep(w.settle)
	}
	return len(p), nil
}

func (w *replyOnWrite) Close() error { return nil }

// eagerTransport builds a stdioTransport wired to a server that answers during
// the write. There is no child process, so the test must not close it.
func eagerTransport(t *testing.T) (*stdioTransport, *replyOnWrite) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})
	stdin := &replyOnWrite{out: pw, settle: 50 * time.Millisecond}
	return &stdioTransport{
		stdin:      stdin,
		stdout:     bufio.NewReaderSize(pr, 1<<20),
		pending:    map[int64]chan *rpcResponse{},
		readerDone: make(chan struct{}),
	}, stdin
}

// TestStdioDeliversReplyThatArrivesBeforeRegistration covers a reply that beats
// its own caller back to the pending map. From the second call onward the
// background reader is already running, so a request registered after its frame
// goes out can have its answer looked up against an id the map does not hold
// yet — the answer is dropped and the caller waits out its whole deadline.
func TestStdioDeliversReplyThatArrivesBeforeRegistration(t *testing.T) {
	tr, _ := eagerTransport(t)

	for id := int64(1); id <= 4; id++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := tr.send(ctx, rpcRequest{JSONRPC: "2.0", ID: id, Method: "tools/call"})
		cancel()
		if err != nil {
			t.Fatalf("call %d was lost: %v", id, err)
		}
		if resp == nil || resp.ID == nil || *resp.ID != id {
			t.Fatalf("call %d answered by %+v, want the reply carrying id %d", id, resp, id)
		}
	}
}

// TestStdioFailedWriteLeavesNoPendingEntry pins the other half of registering
// first: a request whose frame never reached the child must not be left in the
// map, or the next reader error fans out to a caller that is no longer there
// and the entry leaks for the life of the transport.
func TestStdioFailedWriteLeavesNoPendingEntry(t *testing.T) {
	tr, stdin := eagerTransport(t)
	stdin.err = errors.New("broken pipe")

	if _, err := tr.send(context.Background(), rpcRequest{JSONRPC: "2.0", ID: 7, Method: "tools/call"}); err == nil {
		t.Fatal("expected the failed write to surface as an error")
	}

	tr.pendingMu.Lock()
	left := len(tr.pending)
	tr.pendingMu.Unlock()
	if left != 0 {
		t.Fatalf("pending holds %d entries after a failed write, want 0", left)
	}
}

// TestStdioClosedTransportLeavesNoPendingEntry is the same guard for the other
// early return in send.
func TestStdioClosedTransportLeavesNoPendingEntry(t *testing.T) {
	tr, _ := eagerTransport(t)
	tr.closed = true

	if _, err := tr.send(context.Background(), rpcRequest{JSONRPC: "2.0", ID: 9, Method: "tools/call"}); err == nil {
		t.Fatal("expected a closed transport to refuse the call")
	}

	tr.pendingMu.Lock()
	left := len(tr.pending)
	tr.pendingMu.Unlock()
	if left != 0 {
		t.Fatalf("pending holds %d entries after a refused call, want 0", left)
	}
}
