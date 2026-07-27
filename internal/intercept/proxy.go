package intercept

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
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

// Rule matches a request by URL substring and either blocks it or returns a mock
// response, letting the operator shape a target's traffic during testing.
type Rule struct {
	ID         int64  `json:"id"`
	Match      string `json:"match"` // substring of the URL
	Block      bool   `json:"block"`
	MockStatus int    `json:"mock_status"`
	MockBody   string `json:"mock_body"`
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
}

// New builds a proxy backed by the given CA.
func New(ca *CA) *Proxy {
	return &Proxy{
		ca:      ca,
		tr:      &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: false},
		maxLog:  1000,
		maxBody: 256 << 10,
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
