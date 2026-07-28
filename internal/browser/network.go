package browser

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// NetworkRequest is one HTTP request the page issued, paired with its
// response when one was received. Populated by draining CDP Network.*
// events via DrainNetwork.
type NetworkRequest struct {
	RequestID string
	Method    string
	URL       string
	Headers   map[string]string
	PostData  string // request body, when the page supplied one

	// Response is populated when a matching responseReceived event fired
	// before DrainNetwork ran. Nil for failed requests and pre-flight
	// CORS checks that never landed.
	Response *NetworkResponse

	// Failed is non-empty when the request errored (net::ERR_CONNECTION_REFUSED,
	// CORS block, etc). A request with neither Response nor Failed is still
	// in flight or was cancelled mid-action.
	Failed string
}

// NetworkResponse is the wire-level response to a NetworkRequest.
type NetworkResponse struct {
	Status  int
	URL     string // may differ from request URL after redirects
	Headers map[string]string
	// MIME type from response.headers.content-type, captured for filtering.
	MIMEType string
	// RemoteIP is the server address Chrome connected to. Empty for
	// service-worker responses and opaque origins.
	RemoteIP string
}

// networkLedger accumulates Network events between drains. Per-session.
var networkMu sync.Mutex

// DrainNetwork pulls every buffered Network.* event out of the CDP client
// and returns one NetworkRequest per requestID, with request and response
// data merged. Failed requests carry their error string.
//
// The returned slice is in arrival order of requestWillBeSent; responses
// are correlated by requestID. Requests whose response hasn't landed yet
// are still returned with Response == nil — callers can re-drain later or
// accept the partial picture, depending on how long they waited.
//
// DrainNetwork is destructive: a second call returns only what arrived in
// between. The pattern is "run an action, then drain".
func (s *Session) DrainNetwork(ctx context.Context) []NetworkRequest {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		return nil
	}

	requests := map[string]*NetworkRequest{}
	order := []string{}

	// requestWillBeSent — request fired.
	for _, raw := range c.takeEvents("Network.requestWillBeSent") {
		var ev struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL      string            `json:"url"`
				Method   string            `json:"method"`
				Headers  map[string]string `json:"headers"`
				PostData string            `json:"postData"`
			} `json:"request"`
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		// Skip prefetch, beacon, image-cache hits, and the like. Hackbrowser
		// cares about the API surface, not favicon noise.
		if ev.Type != "" && ev.Type != "XHR" && ev.Type != "Fetch" && ev.Type != "Document" {
			continue
		}
		if _, exists := requests[ev.RequestID]; !exists {
			requests[ev.RequestID] = &NetworkRequest{RequestID: ev.RequestID}
			order = append(order, ev.RequestID)
		}
		r := requests[ev.RequestID]
		r.Method = ev.Request.Method
		r.URL = ev.Request.URL
		r.Headers = ev.Request.Headers
		r.PostData = ev.Request.PostData
	}

	// responseReceived — server replied with status + headers.
	for _, raw := range c.takeEvents("Network.responseReceived") {
		var ev struct {
			RequestID string `json:"requestId"`
			Response  struct {
				URL      string            `json:"url"`
				Status   int               `json:"status"`
				Headers  map[string]string `json:"headers"`
				MimeType string            `json:"mimeType"`
				RemoteIP string            `json:"remoteIPAddress"`
			} `json:"response"`
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		if ev.Type != "" && ev.Type != "XHR" && ev.Type != "Fetch" && ev.Type != "Document" {
			continue
		}
		req, ok := requests[ev.RequestID]
		if !ok {
			// Response arrived without a matching request — sometimes happens
			// with service workers or preflight DrainNetwork calls. Track it
			// so the caller at least sees the response.
			req = &NetworkRequest{RequestID: ev.RequestID}
			requests[ev.RequestID] = req
			order = append(order, ev.RequestID)
		}
		req.Response = &NetworkResponse{
			Status:   ev.Response.Status,
			URL:      ev.Response.URL,
			Headers:  ev.Response.Headers,
			MIMEType: ev.Response.MimeType,
			RemoteIP: ev.Response.RemoteIP,
		}
	}

	// loadingFailed — request errored.
	for _, raw := range c.takeEvents("Network.loadingFailed") {
		var ev struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
			Type      string `json:"type"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		req, ok := requests[ev.RequestID]
		if !ok {
			continue
		}
		req.Failed = ev.ErrorText
	}

	out := make([]NetworkRequest, 0, len(order))
	for _, id := range order {
		out = append(out, *requests[id])
	}
	return out
}

// ResponseBody fetches the body of a finished response by requestID. Returns
// (body, mimeType, error). The body may be empty for responses that were
// discarded (large, opaque, or already evicted from Chrome's buffer), so
// callers should treat an empty string + nil error as "no body available"
// rather than as a successful empty body.
//
// Chrome evicts response bodies aggressively; this method works reliably
// only when called shortly after the response arrives.
func (s *Session) ResponseBody(ctx context.Context, requestID string) (body string, mimeType string, err error) {
	raw, err := s.call(ctx, "Network.getResponseBody", map[string]any{"requestId": requestID})
	if err != nil {
		return "", "", err
	}
	var out struct {
		Body       string `json:"body"`
		Base64     bool   `json:"base64Encoded"`
		MimeType   string `json:"mimeType,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", err
	}
	if out.Base64 {
		// Return the base64 form verbatim — the caller knows how to decode.
		// Marking it via the returned mimeType keeps the contract honest.
		return out.Body, "application/base64;" + strings.TrimPrefix(out.MimeType, ""), nil
	}
	return out.Body, out.MimeType, nil
}
