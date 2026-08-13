package llm

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryClient wraps a Client and retries transient failures — rate limits,
// server errors, and dropped connections — with exponential backoff. A model
// call that fails because the provider is briefly overloaded should not surface
// as a hard error the moment a retry would have succeeded.
type retryClient struct {
	inner   Client
	tries   int           // total attempts, including the first
	base    time.Duration // first backoff step; doubles each retry
	maxWait time.Duration // ceiling on any single backoff
}

func newRetrying(inner Client, retries int, base time.Duration) Client {
	if base <= 0 {
		base = time.Second
	}
	return &retryClient{inner: inner, tries: retries + 1, base: base, maxWait: 30 * time.Second}
}

func (c *retryClient) Kind() string { return c.inner.Kind() }

func (c *retryClient) Chat(ctx context.Context, req Request) (*Response, error) {
	var resp *Response
	err := c.withRetry(ctx, func() error {
		var e error
		resp, e = c.inner.Chat(ctx, req)
		return e
	})
	return resp, err
}

// Stream retries only while nothing has been emitted yet. Once tokens have
// reached the caller, a retry would replay a partial answer, so the error is
// surfaced instead.
func (c *retryClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	var resp *Response
	emitted := false
	wrapped := func(e Event) error {
		emitted = true
		return emit(e)
	}
	err := c.withRetry(ctx, func() error {
		if emitted {
			// Past the point of a safe retry: run once more without the guard.
			var e error
			resp, e = c.inner.Stream(ctx, req, wrapped)
			return stopRetry{e}
		}
		var e error
		resp, e = c.inner.Stream(ctx, req, wrapped)
		if e != nil && emitted {
			return stopRetry{e}
		}
		return e
	})
	var sr stopRetry
	if errors.As(err, &sr) {
		return resp, sr.err
	}
	return resp, err
}

func (c *retryClient) Models(ctx context.Context) ([]ModelInfo, error) {
	var out []ModelInfo
	err := c.withRetry(ctx, func() error {
		var e error
		out, e = c.inner.Models(ctx)
		return e
	})
	return out, err
}

func (c *retryClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	var out [][]float32
	err := c.withRetry(ctx, func() error {
		var e error
		out, e = c.inner.Embed(ctx, model, inputs)
		return e
	})
	return out, err
}

// stopRetry marks an error the retry loop must not retry, regardless of kind.
type stopRetry struct{ err error }

func (s stopRetry) Error() string { return s.err.Error() }
func (s stopRetry) Unwrap() error { return s.err }

func (c *retryClient) withRetry(ctx context.Context, call func() error) error {
	var err error
	for attempt := 0; attempt < c.tries; attempt++ {
		err = call()
		if err == nil {
			return nil
		}
		var sr stopRetry
		if errors.As(err, &sr) {
			return err
		}
		if attempt == c.tries-1 || !Retryable(err) {
			return err
		}
		wait := c.backoff(attempt, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}

// backoff returns how long to wait before the next attempt: the provider's
// Retry-After if it sent one, otherwise an exponential step with jitter.
func (c *retryClient) backoff(attempt int, err error) time.Duration {
	if ra := retryAfterOf(err); ra > 0 {
		if ra > c.maxWait {
			return c.maxWait
		}
		return ra
	}
	step := c.base << attempt
	if step > c.maxWait {
		step = c.maxWait
	}
	// Full jitter over [step/2, step] keeps many clients from retrying in lockstep.
	half := step / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// Retryable reports whether an error is worth another attempt: a rate limit, a
// server-side error, or a connection failure. Auth failures and other 4xx are
// permanent and are not retried.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if IsRateLimit(err) || IsUnreachable(err) {
		return true
	}
	// A body that stopped before the provider's terminal marker produced no
	// usable answer, so asking again is safe and usually works.
	if errors.Is(err, ErrStreamTruncated) {
		return true
	}
	var ae *apiError
	if errors.As(err, &ae) {
		if ae.Status >= 500 && ae.Status <= 599 {
			return true
		}
	}
	// Some providers report a transient glitch as a plain error mid-stream —
	// notably truncated/malformed tool_call arguments — and explicitly ask the
	// caller to retry. Honour that when the message says so; permanent errors
	// (bad key, invalid request) do not match these phrases.
	return transientMessage(err.Error())
}

func transientMessage(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range []string{
		"please retry",
		"incomplete tool_call",
		"malformed arguments",
		"malformed tool_call",
		"unterminated string starting at",
		"unexpected end of json input",
		"eof while parsing a string",
		"overloaded",
		"please try again",
		"temporarily unavailable",
	} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// retryAfterOf extracts a Retry-After delay from an apiError, if present.
func retryAfterOf(err error) time.Duration {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.RetryAfter
	}
	return 0
}

// parseRetryAfter reads a Retry-After header value, which is either a number of
// seconds or an HTTP date.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
