package cursor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// errStreamDone signals that the SSE stream ended cleanly with a "done"
// event. It is an internal sentinel, never returned to callers of StreamRun.
var errStreamDone = errors.New("Cursor stream done")

const maxSSELineBytes = 1 << 20 // 1 MiB

// emitError wraps an error returned by the caller-supplied emit callback so
// StreamRun can recognize it and return immediately without reconnecting.
type emitError struct{ err error }

func (e *emitError) Error() string { return e.err.Error() }
func (e *emitError) Unwrap() error { return e.err }

// parseSSE reads Server-Sent Events from r until EOF, a "done" event, or an
// error. It returns the most recent non-empty event id seen (for use as a
// Last-Event-ID header on reconnect) and, if a "result" event was decoded,
// the terminal Run it describes.
func parseSSE(r io.Reader, emit func(StreamEvent) error) (lastID string, terminal *Run, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineBytes)

	var (
		recID   string
		recType string
		recData []string
	)

	flush := func() error {
		if recID == "" && recType == "" && len(recData) == 0 {
			return nil
		}
		if recID != "" {
			lastID = recID
		}
		eventName := recType
		if eventName == "" {
			eventName = "message"
		}
		raw := json.RawMessage(strings.Join(recData, "\n"))
		out := StreamEvent{ID: recID, Type: eventName, Raw: raw}

		var decodeErr error
		switch eventName {
		case "assistant", "thinking":
			var payload struct {
				Text string `json:"text"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			out.Text = payload.Text
		case "status":
			var payload struct {
				RunID  string `json:"runId"`
				Status string `json:"status"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			out.Status = payload.Status
		case "tool_call":
			var payload struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			out.ToolName, out.Status = payload.Name, payload.Status
		case "result":
			var payload struct {
				RunID      string    `json:"runId"`
				Status     string    `json:"status"`
				Text       string    `json:"text"`
				DurationMS int64     `json:"durationMs"`
				Git        *GitState `json:"git,omitempty"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			if decodeErr == nil {
				terminal = &Run{
					ID:         payload.RunID,
					Status:     payload.Status,
					Result:     payload.Text,
					DurationMS: payload.DurationMS,
					Git:        payload.Git,
				}
				out.Status = payload.Status
				out.Text = payload.Text
			}
		case "error":
			var payload struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			if decodeErr == nil {
				return &APIError{Code: payload.Code, Message: payload.Message}
			}
		case "done":
			return errStreamDone
		case "heartbeat", "interaction_update":
			return nil
		}
		if decodeErr != nil {
			return fmt.Errorf("cursor: decode %s event: %w", eventName, decodeErr)
		}
		if emitErr := emit(out); emitErr != nil {
			return &emitError{err: emitErr}
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if ferr := flush(); ferr != nil {
				return lastID, terminal, ferr
			}
			recID, recType, recData = "", "", nil
		case strings.HasPrefix(line, ":"):
			// SSE comment line, typically used as a keep-alive ping.
		case strings.HasPrefix(line, "id:"):
			recID = strings.TrimPrefix(strings.TrimPrefix(line, "id:"), " ")
		case strings.HasPrefix(line, "event:"):
			recType = strings.TrimPrefix(strings.TrimPrefix(line, "event:"), " ")
		case strings.HasPrefix(line, "data:"):
			recData = append(recData, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if serr := scanner.Err(); serr != nil {
		return lastID, terminal, fmt.Errorf("cursor: sse scan: %w", serr)
	}
	if recID != "" || recType != "" || len(recData) != 0 {
		if ferr := flush(); ferr != nil {
			return lastID, terminal, ferr
		}
	}
	return lastID, terminal, nil
}

// streamOnce opens a single SSE connection for a run and parses it until the
// connection closes, a terminal result arrives, or an error occurs.
//
// done reports whether the connection ended in a way that should stop
// reconnect attempts (a "done" event was observed). terminal is non-nil only
// when a "result" event was decoded. err is nil for a clean disconnect that
// callers should retry (no terminal, not done).
func (c *Client) streamOnce(
	ctx context.Context,
	agentID, runID, lastID string,
	emit func(StreamEvent) error,
) (nextID string, terminal *Run, done bool, err error) {
	path := "/v1/agents/" + url.PathEscape(agentID) + "/runs/" + url.PathEscape(runID) + "/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return lastID, nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}

	// Cursor streams rely on periodic heartbeats and can legitimately run
	// far longer than the client's ordinary metadata/lifecycle timeout;
	// lifetime control belongs to ctx instead.
	streamHTTP := *c.http
	streamHTTP.Timeout = 0

	resp, err := streamHTTP.Do(req)
	if err != nil {
		return lastID, nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return lastID, nil, false, c.decodeAPIError(resp)
	}

	id, terminal, err := parseSSE(resp.Body, emit)
	if id != "" {
		lastID = id
	}
	if errors.Is(err, errStreamDone) {
		return lastID, terminal, true, nil
	}
	if err != nil {
		return lastID, terminal, false, err
	}
	return lastID, terminal, false, nil
}

// StreamRun streams a run's events, transparently reconnecting on ordinary
// disconnects (preserving Last-Event-ID) up to a bounded number of attempts.
// It returns the run's terminal state once a "result" event is decoded, or
// once the stream ends and GetRun confirms completion.
func (c *Client) StreamRun(
	ctx context.Context,
	agentID, runID string,
	emit func(StreamEvent) error,
) (*Run, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("cursor: agentID is required")
	}
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("cursor: runID is required")
	}

	backoffs := [3]time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}
	const maxAttempts = 4

	var lastID string
	var resetUsed bool
	attempts := 0
	skipSleep := true

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !skipSleep {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempts-1]):
			}
		}
		skipSleep = false
		attempts++

		nextID, terminal, done, err := c.streamOnce(ctx, agentID, runID, lastID, emit)
		if nextID != "" {
			lastID = nextID
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if IsStatus(err, http.StatusGone) {
				return c.GetRun(ctx, agentID, runID)
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest &&
				apiErr.Code == "invalid_last_event_id" && !resetUsed {
				resetUsed = true
				lastID = ""
				attempts--
				skipSleep = true
				continue
			}
			var emErr *emitError
			if errors.As(err, &emErr) {
				return nil, emErr.err
			}
			return nil, err
		}
		if terminal != nil {
			return terminal, nil
		}
		if done {
			// Stream ended with "done" but no "result" event; confirm the
			// final state via the metadata API instead of guessing.
			return c.GetRun(ctx, agentID, runID)
		}
		if attempts >= maxAttempts {
			return nil, fmt.Errorf("cursor: stream run did not complete after %d attempts", maxAttempts)
		}
	}
}
