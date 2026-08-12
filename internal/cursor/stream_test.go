package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
