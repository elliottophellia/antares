package intercept

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Exchange is one captured request/response pair.
type Exchange struct {
	ID          int64       `json:"id"`
	Time        time.Time   `json:"time"`
	Method      string      `json:"method"`
	URL         string      `json:"url"`
	Host        string      `json:"host"`
	Secure      bool        `json:"secure"`
	ReqHeaders  http.Header `json:"req_headers"`
	ReqBody     string      `json:"req_body"`
	Status      int         `json:"status"`
	RespHeaders http.Header `json:"resp_headers"`
	RespBody    string      `json:"resp_body"`
	DurationMS  int64       `json:"duration_ms"`
	Mocked      bool        `json:"mocked"`
	Blocked     bool        `json:"blocked"`
}

// Rule matches a request by URL substring and either blocks it, returns a mock
// response, or pauses it at a breakpoint for the operator to edit — letting them
// shape a target's traffic during testing.
type Rule struct {
	ID         int64  `json:"id"`
	Match      string `json:"match"` // substring of the URL
	Block      bool   `json:"block"`
	MockStatus int    `json:"mock_status"`
	MockBody   string `json:"mock_body"`
	// Breakpoint pauses a matching request before it is forwarded, so the
	// operator can edit it (or the response) and then resume or abort it.
	Breakpoint bool `json:"breakpoint"`
}

// PausedExchange is a request held at a breakpoint, waiting for the operator to
// resume (optionally with edits) or abort it. It carries a resolve channel the
// waiting proxy goroutine blocks on.
type PausedExchange struct {
	ID      int64             `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	resolve chan breakpointEdit
}

// breakpointEdit is how a paused exchange is released: either aborted, or
// resumed with (possibly edited) request fields.
type breakpointEdit struct {
	abort   bool
	method  string
	url     string
	headers map[string]string
	body    string
}

// BreakpointResume carries operator edits to release a paused request.
type BreakpointResume struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Proxy is a MITM HTTP(S) proxy that captures every exchange.
type Proxy struct {
	ca      *CA
	tr      *http.Transport
	maxLog  int
	maxBody int64

	mu       sync.RWMutex
	log      []*Exchange
	rules    []Rule
	nextID   int64
	ruleID   int64
	running  bool
	addr     string
	listener net.Listener

	// Breakpoints: requests paused mid-flight, keyed by exchange id, plus a
	// pub/sub so the dashboard learns of a new pause without polling.
	pausedID int64
	paused   map[int64]*PausedExchange
	bpMu     sync.Mutex
	bpSubs   map[int]chan struct{}
	bpSeq    int
}

// New builds a proxy backed by the given CA.
func New(ca *CA) *Proxy {
	return &Proxy{
		ca:      ca,
		tr:      &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: false},
		maxLog:  1000,
		maxBody: 256 << 10,
		paused:  map[int64]*PausedExchange{},
		bpSubs:  map[int]chan struct{}{},
	}
}

// Start begins listening on addr (e.g. "127.0.0.1:8899").
func (p *Proxy) Start(addr string) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return fmt.Errorf("proxy already running on %s", p.addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	p.listener, p.addr, p.running = ln, ln.Addr().String(), true
	p.mu.Unlock()

	srv := &http.Server{Handler: http.HandlerFunc(p.serve)}
	go srv.Serve(ln)
	return nil
}

// Stop shuts the proxy down.
func (p *Proxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		p.listener.Close()
	}
	p.running = false
}

// Status reports whether the proxy is running and where.
func (p *Proxy) Status() (bool, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running, p.addr
}

// Exchanges returns a snapshot of the captured log (newest first).
func (p *Proxy) Exchanges() []*Exchange {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Exchange, len(p.log))
	for i, e := range p.log {
		out[len(p.log)-1-i] = e
	}
	return out
}

// Clear empties the capture log.
func (p *Proxy) Clear() {
	p.mu.Lock()
	p.log = nil
	p.mu.Unlock()
}

// Rules returns the active rules.
func (p *Proxy) Rules() []Rule {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Rule(nil), p.rules...)
}

// AddRule registers a rule and returns it with an assigned id.
func (p *Proxy) AddRule(r Rule) Rule {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ruleID++
	r.ID = p.ruleID
	p.rules = append(p.rules, r)
	return r
}

// DeleteRule removes a rule by id.
func (p *Proxy) DeleteRule(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.rules[:0]
	for _, r := range p.rules {
		if r.ID != id {
			out = append(out, r)
		}
	}
	p.rules = out
}

// CACertPEM exposes the CA cert for the user to trust.
func (p *Proxy) CACertPEM() []byte { return p.ca.CertPEM() }

// CA returns the proxy's certificate authority.
func (p *Proxy) CA() *CA { return p.ca }

// SPKIFingerprint is the CA's SubjectPublicKeyInfo fingerprint, for launching
// Chromium-family browsers that trust the proxy without a cert install.
func (p *Proxy) SPKIFingerprint() string { return p.ca.SPKIFingerprint() }

func (p *Proxy) record(e *Exchange) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.ID = atomic.AddInt64(&p.nextID, 1)
	p.log = append(p.log, e)
	if len(p.log) > p.maxLog {
		p.log = p.log[len(p.log)-p.maxLog:]
	}
}

func (p *Proxy) matchRule(url string) (Rule, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, r := range p.rules {
		if r.Match != "" && strings.Contains(url, r.Match) {
			return r, true
		}
	}
	return Rule{}, false
}

// breakpointTimeout bounds how long a request may sit paused, so a client
// connection is never wedged indefinitely if nobody resolves the breakpoint.
const breakpointTimeout = 5 * time.Minute

// ListPaused returns the requests currently held at breakpoints.
func (p *Proxy) ListPaused() []*PausedExchange {
	p.bpMu.Lock()
	defer p.bpMu.Unlock()
	out := make([]*PausedExchange, 0, len(p.paused))
	for _, pe := range p.paused {
		out = append(out, pe)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Resume releases a paused request with the operator's edits and lets the proxy
// forward it. Unknown ids are a no-op.
func (p *Proxy) Resume(id int64, edit BreakpointResume) {
	p.bpMu.Lock()
	pe := p.paused[id]
	p.bpMu.Unlock()
	if pe == nil {
		return
	}
	pe.resolve <- breakpointEdit{method: edit.Method, url: edit.URL, headers: edit.Headers, body: edit.Body}
}

// Abort releases a paused request by refusing it (the client gets a 4xx).
func (p *Proxy) Abort(id int64) {
	p.bpMu.Lock()
	pe := p.paused[id]
	p.bpMu.Unlock()
	if pe == nil {
		return
	}
	pe.resolve <- breakpointEdit{abort: true}
}

// SubscribeBreakpoints returns a channel that fires whenever a request is paused
// or resolved, so the dashboard can refresh without polling.
func (p *Proxy) SubscribeBreakpoints() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	p.bpMu.Lock()
	p.bpSeq++
	id := p.bpSeq
	p.bpSubs[id] = ch
	p.bpMu.Unlock()
	return ch, func() {
		p.bpMu.Lock()
		if c, ok := p.bpSubs[id]; ok {
			delete(p.bpSubs, id)
			close(c)
		}
		p.bpMu.Unlock()
	}
}

func (p *Proxy) notifyBreakpoints() {
	p.bpMu.Lock()
	defer p.bpMu.Unlock()
	for _, ch := range p.bpSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// pauseAtBreakpoint holds a request until the operator resumes or aborts it (or
// the timeout elapses). It returns the edited request fields and whether the
// request should be aborted. headers is a flat map for the UI's convenience.
func (p *Proxy) pauseAtBreakpoint(method, url string, headers map[string]string, body string) (breakpointEdit, bool) {
	pe := &PausedExchange{
		Method: method, URL: url, Headers: headers, Body: body,
		resolve: make(chan breakpointEdit, 1),
	}
	p.bpMu.Lock()
	p.pausedID++
	pe.ID = p.pausedID
	p.paused[pe.ID] = pe
	p.bpMu.Unlock()
	p.notifyBreakpoints()

	defer func() {
		p.bpMu.Lock()
		delete(p.paused, pe.ID)
		p.bpMu.Unlock()
		p.notifyBreakpoints()
	}()

	select {
	case edit := <-pe.resolve:
		return edit, true
	case <-time.After(breakpointTimeout):
		return breakpointEdit{}, false // timed out — proceed unmodified
	}
}

// flatHeaders collapses an http.Header to a single-value map for the breakpoint
// editor; rebuildHeaders does the reverse when applying edits.
func flatHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k := range h {
		out[k] = h.Get(k)
	}
	return out
}

func rebuildHeaders(m map[string]string) http.Header {
	h := http.Header{}
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handlePlain(w, r)
}

// handlePlain proxies (and captures) a plain-HTTP request.
func (p *Proxy) handlePlain(w http.ResponseWriter, r *http.Request) {
	e := p.forward(r, false)
	if e == nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	writeCaptured(w, e)
}

// handleConnect MITMs an HTTPS tunnel: it terminates TLS with a signed leaf,
// then serves the inner requests, forwarding each to the origin.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	tlsConn := tls.Server(clientConn, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = host
			}
			return p.ca.LeafFor(name)
		},
	})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer tlsConn.Close()

	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		req.URL.Scheme = "https"
		req.URL.Host = r.Host
		req.RequestURI = ""
		e := p.forward(req, true)
		if e == nil {
			return
		}
		// Reconstruct a response onto the TLS conn.
		if err := writeExchangeResponse(tlsConn, e); err != nil {
			return
		}
		if req.Close || e.RespHeaders.Get("Connection") == "close" {
			return
		}
	}
}

// forward sends the request to the origin (or applies a rule) and records it.
func (p *Proxy) forward(req *http.Request, secure bool) *Exchange {
	start := time.Now()
	fullURL := req.URL.String()
	if req.URL.Host == "" {
		fullURL = req.Host + req.URL.RequestURI()
	}
	reqBody, _ := readCap(req.Body, p.maxBody)
	e := &Exchange{
		Time: start, Method: req.Method, URL: fullURL, Host: req.Host, Secure: secure,
		ReqHeaders: req.Header.Clone(), ReqBody: string(reqBody),
		RespHeaders: http.Header{},
	}

	// Rule interception: block or mock before touching the network.
	if rule, ok := p.matchRule(fullURL); ok {
		if rule.Block {
			e.Blocked, e.Status = true, 599
			e.RespBody = "blocked by antares intercept rule"
			e.DurationMS = time.Since(start).Milliseconds()
			p.record(e)
			return e
		}
		if rule.Breakpoint {
			// Pause for the operator; apply edits or abort when they resume.
			edit, resolved := p.pauseAtBreakpoint(req.Method, fullURL, flatHeaders(req.Header), string(reqBody))
			if resolved && edit.abort {
				e.Blocked, e.Status = true, 599
				e.RespBody = "aborted at breakpoint"
				e.DurationMS = time.Since(start).Milliseconds()
				p.record(e)
				return e
			}
			if resolved {
				if edit.method != "" {
					req.Method, e.Method = edit.method, edit.method
				}
				if edit.url != "" {
					if u, err := req.URL.Parse(edit.url); err == nil {
						req.URL = u
						fullURL, e.URL = edit.url, edit.url
					}
				}
				if edit.headers != nil {
					req.Header = rebuildHeaders(edit.headers)
					e.ReqHeaders = req.Header.Clone()
				}
				if edit.body != "" {
					reqBody = []byte(edit.body)
					e.ReqBody = edit.body
				}
			}
			// Fall through to the normal forward with the (edited) request.
		} else {
			e.Mocked = true
			e.Status = rule.MockStatus
			if e.Status == 0 {
				e.Status = 200
			}
			e.RespBody = rule.MockBody
			e.RespHeaders.Set("Content-Type", "text/plain")
			e.DurationMS = time.Since(start).Milliseconds()
			p.record(e)
			return e
		}
	}

	// Rebuild a clean outbound request body.
	outReq := req.Clone(req.Context())
	outReq.Body = io.NopCloser(strings.NewReader(string(reqBody)))
	outReq.ContentLength = int64(len(reqBody))
	resp, err := p.tr.RoundTrip(outReq)
	if err != nil {
		e.Status = 502
		e.RespBody = "upstream error: " + err.Error()
		e.DurationMS = time.Since(start).Milliseconds()
		p.record(e)
		return e
	}
	defer resp.Body.Close()
	respBody, _ := readCap(resp.Body, p.maxBody)
	e.Status = resp.StatusCode
	e.RespHeaders = resp.Header.Clone()
	e.RespBody = string(respBody)
	e.DurationMS = time.Since(start).Milliseconds()
	p.record(e)
	return e
}

func readCap(r io.Reader, max int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r, max))
}

// writeCaptured writes an exchange back to a standard ResponseWriter.
func writeCaptured(w http.ResponseWriter, e *Exchange) {
	for k, vs := range e.RespHeaders {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if e.Status == 0 {
		e.Status = 200
	}
	w.WriteHeader(e.Status)
	io.WriteString(w, e.RespBody)
}

// writeExchangeResponse serialises an exchange as an HTTP/1.1 response onto conn.
func writeExchangeResponse(conn net.Conn, e *Exchange) error {
	var b strings.Builder
	status := e.Status
	if status == 0 {
		status = 200
	}
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	e.RespHeaders.Del("Transfer-Encoding")
	e.RespHeaders.Del("Content-Length")
	for k, vs := range e.RespHeaders {
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(e.RespBody))
	b.WriteString("\r\n")
	b.WriteString(e.RespBody)
	_, err := conn.Write([]byte(b.String()))
	return err
}
