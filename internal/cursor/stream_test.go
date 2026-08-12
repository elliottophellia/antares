package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStreamRunReconnectsFromLastEventID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hello\"}\n\n")
		default:
			if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
				t.Fatalf("Last-Event-ID = %q", got)
			}
			_, _ = io.WriteString(w,
				"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
					"id: evt-3\nevent: done\ndata: {}\n\n")
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestStreamRunReconnectsBeyondAttemptBudgetWhenEachDisconnectAdvancesEventID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		attempt := calls.Add(1)
		if attempt > 1 {
			want := fmt.Sprintf("evt-%d", attempt-1)
			if got := r.Header.Get("Last-Event-ID"); got != want {
				t.Errorf("attempt %d Last-Event-ID = %q, want %q", attempt, got, want)
			}
		}
		if attempt <= 5 {
			_, _ = fmt.Fprintf(w,
				"id: evt-%d\nevent: assistant\ndata: {\"text\":\"progress\"}\n\n",
				attempt,
			)
			return
		}
		_, _ = io.WriteString(w,
			"id: evt-6\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-7\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if calls.Load() != 6 {
		t.Fatalf("stream calls = %d, want 6", calls.Load())
	}
	if len(events) != 6 {
		t.Fatalf("events = %d, want five progress events and one result", len(events))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// truncatedBody serves fixed SSE bytes and then fails, standing in for a
// connection dropped mid-stream.
type truncatedBody struct {
	data []byte
	err  error
	off  int
}

func (b *truncatedBody) Read(p []byte) (int, error) {
	if b.off < len(b.data) {
		n := copy(p, b.data[b.off:])
		b.off += n
		return n, nil
	}
	return 0, b.err
}

func (b *truncatedBody) Close() error { return nil }

func truncatedStreamClient(t *testing.T, respond func(attempt int32, r *http.Request) (string, error)) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	client, err := New(Options{
		BaseURL: "https://api.cursor.invalid",
		APIKey:  "synthetic-key",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, readErr := respond(calls.Add(1), r)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       &truncatedBody{data: []byte(body), err: readErr},
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestStreamRunReconnectsAfterConnectionResetWithLastEventID(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	client, calls := truncatedStreamClient(t, func(attempt int32, r *http.Request) (string, error) {
		if attempt == 1 {
			return "id: evt-1\nevent: assistant\ndata: {\"text\":\"hello\"}\n\n", reset
		}
		if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
			t.Errorf("reconnect Last-Event-ID = %q, want evt-1", got)
		}
		return "id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n" +
			"id: evt-3\nevent: done\ndata: {}\n\n", io.EOF
	})

	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v; want reconnect after a connection reset", run, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("stream calls = %d, want 2", calls.Load())
	}
}

func TestStreamRunTerminalResultWinsOverLaterReadError(t *testing.T) {
	client, calls := truncatedStreamClient(t, func(int32, *http.Request) (string, error) {
		return "id: evt-1\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n",
			io.ErrUnexpectedEOF
	})

	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v; want the decoded result to outrank the read error", run, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("stream calls = %d, want 1", calls.Load())
	}
}

// The same recovery must hold over a real connection, not just an injected
// read error: an aborted response truncates the chunked body mid-stream.
func TestStreamRunReconnectsAfterTruncatedResponse(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hello\"}\n\n")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
			t.Errorf("reconnect Last-Event-ID = %q, want evt-1", got)
		}
		_, _ = io.WriteString(w,
			"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-3\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v; want reconnect after a truncated response", run, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("stream calls = %d, want 2", calls.Load())
	}
}

func TestStreamRunDoesNotRetryInvalidPayload(t *testing.T) {
	client, calls := truncatedStreamClient(t, func(int32, *http.Request) (string, error) {
		return "id: evt-1\nevent: result\ndata: {\"runId\":\n\n", io.EOF
	})

	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err == nil {
		t.Fatalf("StreamRun = %+v, nil; want an immediate decode error", run)
	}
	if calls.Load() != 1 {
		t.Fatalf("stream calls = %d, want 1 (invalid payloads are not retried)", calls.Load())
	}
}

// Reconnects after read failures reuse the bounded no-progress budget rather
// than looping until the context expires.
func TestStreamRunTruncatedReadsRespectNoProgressBudget(t *testing.T) {
	var streamCalls, statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done via status",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Result != "done via status" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if streamCalls.Load() != 4 || statusCalls.Load() != 1 {
		t.Fatalf("stream calls = %d, status calls = %d; want 4 and 1", streamCalls.Load(), statusCalls.Load())
	}
}

func TestStreamRunNoProgressCapFallsBackToTerminalRun(t *testing.T) {
	var streamCalls, statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done via status",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error {
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "done via status" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if streamCalls.Load() != 4 || statusCalls.Load() != 1 {
		t.Fatalf("stream calls = %d, status calls = %d; want 4 and 1", streamCalls.Load(), statusCalls.Load())
	}
}

func TestStreamRunNoProgressFallbackDoesNotReturnActiveRun(t *testing.T) {
	var streamCalls, statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "RUNNING",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(ctx, "bc-agent", "run-one", func(StreamEvent) error {
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StreamRun = %+v, %v; want context deadline after active fallback", run, err)
	}
	if statusCalls.Load() == 0 {
		t.Fatal("no-progress cap never checked run status")
	}
	if got := streamCalls.Load(); got < 4 || got > 6 {
		t.Fatalf("stream calls = %d, want bounded reconnects after fallback", got)
	}
	if got := statusCalls.Load(); got != 1 {
		t.Fatalf("status calls = %d, want one bounded fallback check", got)
	}
}

// Cursor computes durationMs "once the run reaches FINISHED, ERROR,
// CANCELLED, or EXPIRED" — Cloud Agents API, "Get A Run"
// (https://cursor.com/docs/cloud-agent/api/endpoints).
func TestIsTerminalRunStatusCoversEveryCursorTerminalState(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{status: "FINISHED", want: true},
		{status: "ERROR", want: true},
		{status: "CANCELLED", want: true},
		{status: "EXPIRED", want: true},
		{status: " expired ", want: true},
		{status: "RUNNING"},
		{status: "CREATING"},
		{status: "PENDING"},
		{status: ""},
	} {
		if got := isTerminalRunStatus(tc.status); got != tc.want {
			t.Errorf("isTerminalRunStatus(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestStreamRunNoProgressFallbackReturnsExpiredRun(t *testing.T) {
	var statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "EXPIRED",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(ctx, "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "EXPIRED" {
		t.Fatalf("StreamRun = %+v, %v; want the EXPIRED run returned instead of reconnecting", run, err)
	}
	if statusCalls.Load() != 1 {
		t.Fatalf("status calls = %d, want one bounded fallback check", statusCalls.Load())
	}
}

func TestStreamRunUsesDocumentedStreamEndpoint(t *testing.T) {
	var gotMethod, gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotURI = r.Method, r.RequestURI
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"id: evt-1\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-2\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	if _, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil }); err != nil {
		t.Fatalf("StreamRun error = %v", err)
	}
	// Cursor Cloud Agents API, "Stream A Run":
	// https://cursor.com/docs/cloud-agent/api/endpoints#stream-a-run
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want GET", gotMethod)
	}
	if want := "/v1/agents/bc-agent/runs/run-one/stream"; gotURI != want {
		t.Fatalf("request URI = %q, want %q", gotURI, want)
	}
}

func TestStreamRunParsesMultilineDataToolCallAndIgnoresHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			": ping\n\n"+
				"event: heartbeat\ndata: {}\n\n"+
				"id: evt-1\nevent: tool_call\ndata: {\"name\":\"grep\",\"status\":\"running\"}\n\n"+
				"id: evt-2\nevent: assistant\ndata: {\"text\":\ndata: \"hello world\"}\n\n"+
				"id: evt-3\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"ok\"}\n\n"+
				"id: evt-4\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "ok" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v, want 3 (heartbeat and done must be ignored)", events)
	}
	if events[0].Type != "tool_call" || events[0].ToolName != "grep" || events[0].Status != "running" {
		t.Fatalf("tool_call event = %+v", events[0])
	}
	if events[1].Type != "assistant" || events[1].Text != "hello world" {
		t.Fatalf("assistant event (multiline data) = %+v", events[1])
	}
}

// The tool layer redacts again, but the client contract must on its own keep
// the configured key out of stream errors and keep them bounded.
func TestStreamRunSanitizesInBandSSEError(t *testing.T) {
	const key = "synthetic-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt-1\nevent: error\ndata: {\"code\":\"rejected "+key+
			"\",\"message\":\"upstream refused "+key+" \xff\xfe "+strings.Repeat("padding ", 400)+"\"}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	_, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("StreamRun accepted an SSE error event")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if strings.Contains(apiErr.Message, key) || strings.Contains(apiErr.Code, key) ||
		strings.Contains(err.Error(), key) {
		t.Fatalf("stream error leaked the API key: code=%q message=%q", apiErr.Code, apiErr.Message)
	}
	if got := utf8.RuneCountInString(apiErr.Message); got > 240 {
		t.Fatalf("stream error message = %d runes, want the bounded API-error policy", got)
	}
	if !utf8.ValidString(apiErr.Message) || !utf8.ValidString(apiErr.Code) {
		t.Fatalf("stream error was not normalized to valid UTF-8: code=%q message=%q", apiErr.Code, apiErr.Message)
	}
}

func TestStreamRunContextCancellationReturnsImmediately(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-block
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := client.StreamRun(ctx, "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("StreamRun took too long to honor cancellation: %v", elapsed)
	}
}

func TestStreamRunOversizedLineReturnsExplicitError(t *testing.T) {
	huge := strings.Repeat("a", 2<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: assistant\ndata: "+huge+"\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	_, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("expected explicit error for oversized SSE line, got nil (possible silent partial success)")
	}
}

func TestStreamRun410FallsBackToGetRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "stream_expired", "message": "stream expired"})
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
}

func TestStreamRunEndsWithDoneButNoResultFallsBackToGetRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "id: evt-1\nevent: done\ndata: {}\n\n")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done via fallback",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Result != "done via fallback" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
}

func TestStreamRunResetsOnceAfterInvalidLastEventID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hi\"}\n\n")
		case 2:
			if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
				t.Fatalf("Last-Event-ID = %q, want evt-1", got)
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "invalid_last_event_id", "message": "unknown event id"})
		case 3:
			if got := r.Header.Get("Last-Event-ID"); got != "" {
				t.Fatalf("Last-Event-ID = %q, want reset to empty", got)
			}
			_, _ = io.WriteString(w,
				"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
					"id: evt-3\nevent: done\ndata: {}\n\n")
		default:
			t.Fatalf("unexpected extra call %d", calls.Load())
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestStreamRunReturnsEmitErrorImmediately(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hi\"}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	wantErr := errors.New("synthetic emit failure")
	_, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after emit error)", calls.Load())
	}
}

func TestStreamRunIgnoresClientTimeoutDuringStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hi\"}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(120 * time.Millisecond)
		_, _ = io.WriteString(w,
			"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-3\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" {
		t.Fatalf("StreamRun = %+v, %v (client timeout should not apply mid-stream)", run, err)
	}
}
