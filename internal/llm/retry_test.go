package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClient records how many times each method was called and fails a set
// number of times before succeeding.
type fakeClient struct {
	calls   int
	failN   int
	failErr error
}

func (f *fakeClient) Kind() string { return "fake" }
func (f *fakeClient) Chat(ctx context.Context, req Request) (*Response, error) {
	f.calls++
	if f.calls <= f.failN {
		return nil, f.failErr
	}
	return &Response{Content: "ok"}, nil
}
func (f *fakeClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	f.calls++
	if f.calls <= f.failN {
		return nil, f.failErr
	}
	return &Response{Content: "ok"}, nil
}
func (f *fakeClient) Models(ctx context.Context) ([]ModelInfo, error) { return nil, nil }
func (f *fakeClient) Embed(ctx context.Context, m string, in []string) ([][]float32, error) {
	return nil, nil
}

func fastRetry(inner Client, retries int) *retryClient {
	rc := newRetrying(inner, retries, time.Millisecond).(*retryClient)
	rc.maxWait = 5 * time.Millisecond
	return rc
}

func TestRetrySucceedsAfterTransient(t *testing.T) {
	f := &fakeClient{failN: 2, failErr: &apiError{Status: 503}}
	rc := fastRetry(f, 3)
	resp, err := rc.Chat(context.Background(), Request{})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if resp.Content != "ok" || f.calls != 3 {
		t.Fatalf("expected 3 calls and ok, got calls=%d resp=%v", f.calls, resp)
	}
}

func TestRetryGivesUpAfterLimit(t *testing.T) {
	f := &fakeClient{failN: 10, failErr: &apiError{Status: 429}}
	rc := fastRetry(f, 2)
	_, err := rc.Chat(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected failure after exhausting retries")
	}
	if f.calls != 3 { // 1 initial + 2 retries
		t.Fatalf("expected 3 attempts, got %d", f.calls)
	}
}

func TestAuthErrorNotRetried(t *testing.T) {
	f := &fakeClient{failN: 10, failErr: &apiError{Status: 401}}
	rc := fastRetry(f, 5)
	_, err := rc.Chat(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if f.calls != 1 {
		t.Fatalf("auth error must not be retried, got %d calls", f.calls)
	}
}

func TestRetryable(t *testing.T) {
	if !Retryable(&apiError{Status: 429}) {
		t.Fatal("429 is retryable")
	}
	if !Retryable(&apiError{Status: 500}) || !Retryable(&apiError{Status: 503}) {
		t.Fatal("5xx is retryable")
	}
	if Retryable(&apiError{Status: 401}) || Retryable(&apiError{Status: 400}) {
		t.Fatal("4xx (non-429) is not retryable")
	}
	if Retryable(nil) {
		t.Fatal("nil is not retryable")
	}
}

func TestParseRetryAfterSeconds(t *testing.T) {
	if got := parseRetryAfter("5"); got != 5*time.Second {
		t.Fatalf("expected 5s, got %v", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Fatalf("empty should be 0, got %v", got)
	}
	if got := parseRetryAfter("garbage"); got != 0 {
		t.Fatalf("garbage should be 0, got %v", got)
	}
}

func TestBackoffHonoursRetryAfter(t *testing.T) {
	rc := fastRetry(&fakeClient{}, 1)
	rc.maxWait = time.Hour
	d := rc.backoff(0, &apiError{Status: 429, RetryAfter: 2 * time.Second})
	if d != 2*time.Second {
		t.Fatalf("expected Retry-After to win, got %v", d)
	}
}

func TestStreamNotRetriedAfterEmit(t *testing.T) {
	// A stream that emits then fails must not be retried (would replay output).
	emitThenFail := &streamFake{failErr: &apiError{Status: 503}}
	rc := fastRetry(emitThenFail, 3)
	_, err := rc.Stream(context.Background(), Request{}, func(Event) error { return nil })
	if err == nil {
		t.Fatal("expected the post-emit error to surface")
	}
	if emitThenFail.calls != 1 {
		t.Fatalf("stream must not retry after emitting, got %d calls", emitThenFail.calls)
	}
}

type streamFake struct {
	calls   int
	failErr error
}

func (s *streamFake) Kind() string                                     { return "fake" }
func (s *streamFake) Chat(context.Context, Request) (*Response, error) { return nil, nil }
func (s *streamFake) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	s.calls++
	_ = emit(Event{}) // emit something first
	return nil, s.failErr
}
func (s *streamFake) Models(context.Context) ([]ModelInfo, error) { return nil, nil }
func (s *streamFake) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

// compile-time: stopRetry is an error that unwraps.
var _ interface{ Unwrap() error } = stopRetry{err: errors.New("x")}
