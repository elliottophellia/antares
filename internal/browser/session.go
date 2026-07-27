package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/stealth"
)

// Session is one browser with one active page. Keeping the page alive between
// tool calls is the whole point: an agent clicks, reads, and clicks again
// without re-navigating and losing its login.
type Session struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	conn      *conn
	pageSess  string // CDP sessionId for the attached page
	targetID  string
	opts      Options
	lastUsed  time.Time
	started   bool
	userAgent string
}

// New builds an unstarted session.
func New(opts Options) *Session {
	if opts.Width <= 0 {
		opts.Width = 1280
	}
	if opts.Height <= 0 {
		opts.Height = 800
	}
	return &Session{opts: opts}
}

// Started reports whether a browser is running.
func (s *Session) Started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// LastUsed is when a command last ran, for idle reaping.
func (s *Session) LastUsed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

// Start launches or attaches to a browser and opens a page.
func (s *Session) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}

	base := s.opts.RemoteURL
	if base == "" {
		port, err := freePort()
		if err != nil {
			return err
		}
		dir := s.opts.UserDataDir
		if dir == "" {
			dir, err = os.MkdirTemp("", "antares-browser-")
			if err != nil {
				return err
			}
		}
		// Flags the caller owns regardless of which binary launches: the debug
		// port, the profile dir, and the window. The stealth build supplies its
		// own fingerprint/sandbox flags on top of these.
		args := []string{
			"--remote-debugging-port=" + strconv.Itoa(port),
			"--user-data-dir=" + dir,
			"--no-first-run", "--no-default-browser-check",
			"--disable-background-networking", "--disable-sync",
			// Containers and root users cannot use the sandbox; without this
			// Chrome exits immediately on many servers.
			"--no-sandbox", "--disable-dev-shm-usage",
			"--window-size=" + strconv.Itoa(s.opts.Width) + "," + strconv.Itoa(s.opts.Height),
			"about:blank",
		}
		if s.opts.Headless {
			args = append([]string{"--headless=new", "--disable-gpu", "--hide-scrollbars"}, args...)
		}

		var bin string
		if s.opts.Stealth {
			bin, err = stealth.EnsureBinary(ctx, "")
			if err != nil {
				return fmt.Errorf("could not obtain the stealth browser: %w", err)
			}
			// Prepend the anti-detection flags so the patched binary applies
			// them; the caller-owned flags above still win where they overlap.
			stealthArgs := stealth.Options{
				Proxy:    s.opts.Proxy,
				Timezone: s.opts.Timezone,
				Locale:   s.opts.Locale,
			}.Args()
			args = append(stealthArgs, args...)
		} else {
			bin, err = FindExecutable(s.opts.Executable)
			if err != nil {
				return err
			}
		}
		cmd := exec.Command(bin, args...)
		cmd.Stdout, cmd.Stderr = nil, nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("could not start the browser: %w", err)
		}
		s.cmd = cmd
		base = "http://127.0.0.1:" + strconv.Itoa(port)
	}

	// Chrome opens its debug port a moment after the process starts.
	var info versionInfo
	deadline := time.Now().Add(20 * time.Second)
	for {
		var err error
		info, err = fetchVersion(ctx, base)
		if err == nil && info.WebSocketDebuggerURL != "" {
			break
		}
		if time.Now().After(deadline) {
			s.killLocked()
			return errors.New("the browser did not open its debugging port in time")
		}
		select {
		case <-ctx.Done():
			s.killLocked()
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}

	c, err := dial(info.WebSocketDebuggerURL)
	if err != nil {
		s.killLocked()
		return err
	}
	s.conn = c
	s.userAgent = info.Browser

	raw, err := c.send(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		s.killLocked()
		return err
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(raw, &created)
	s.targetID = created.TargetID

	raw, err = c.send(ctx, "", "Target.attachToTarget", map[string]any{
		"targetId": created.TargetID, "flatten": true,
	})
	if err != nil {
		s.killLocked()
		return err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(raw, &attached)
	s.pageSess = attached.SessionID

	for _, m := range []string{"Page.enable", "Runtime.enable", "DOM.enable", "Network.enable", "Log.enable"} {
		if _, err := c.send(ctx, s.pageSess, m, nil); err != nil {
			s.killLocked()
			return err
		}
	}
	_, _ = c.send(ctx, s.pageSess, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": s.opts.Width, "height": s.opts.Height, "deviceScaleFactor": 1, "mobile": false,
	})

	s.started = true
	s.lastUsed = time.Now()
	return nil
}

// Stop closes the page and the browser it started.
func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.killLocked()
}

func (s *Session) killLocked() {
	if s.conn != nil {
		s.conn.shutdown()
		s.conn = nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
		s.cmd = nil
	}
	s.started = false
	s.pageSess = ""
	s.targetID = ""
}

// call runs one CDP command against the page, starting the browser if needed.
func (s *Session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := s.Start(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	c, sess := s.conn, s.pageSess
	s.lastUsed = time.Now()
	s.mu.Unlock()
	if c == nil {
		return nil, errors.New("the browser is not running")
	}
	return c.send(ctx, sess, method, params)
}

// Eval runs JavaScript in the page and returns its value as JSON.
func (s *Session) Eval(ctx context.Context, expression string) (json.RawMessage, error) {
	raw, err := s.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.ExceptionDetails != nil {
		msg := out.ExceptionDetails.Text
		if out.ExceptionDetails.Exception != nil && out.ExceptionDetails.Exception.Description != "" {
			msg = out.ExceptionDetails.Exception.Description
		}
		return nil, errors.New(msg)
	}
	return out.Result.Value, nil
}

// EvalString runs JavaScript expected to produce a string.
func (s *Session) EvalString(ctx context.Context, expression string) (string, error) {
	v, err := s.Eval(ctx, expression)
	if err != nil {
		return "", err
	}
	var out string
	if json.Unmarshal(v, &out) == nil {
		return out, nil
	}
	return string(v), nil
}

// Navigate loads a URL and waits for the page to settle.
func (s *Session) Navigate(ctx context.Context, url string) error {
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	if _, err := s.call(ctx, "Page.navigate", map[string]any{"url": url}); err != nil {
		return err
	}
	return s.WaitReady(ctx, 20*time.Second)
}

// WaitReady blocks until the document has finished parsing, or the timeout
// passes. A page that never settles is still usable, so a timeout is not an
// error the caller has to handle.
func (s *Session) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := s.EvalString(ctx, "document.readyState")
		if err == nil && (state == "interactive" || state == "complete") {
			// A short settle lets client-rendered pages paint their first view.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(350 * time.Millisecond):
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil
}

// URL reports where the page currently is.
func (s *Session) URL(ctx context.Context) (string, error) {
	return s.EvalString(ctx, "location.href")
}

// Title reports the page title.
func (s *Session) Title(ctx context.Context) (string, error) {
	return s.EvalString(ctx, "document.title")
}

// Screenshot captures the viewport as PNG bytes.
func (s *Session) Screenshot(ctx context.Context, fullPage bool) ([]byte, error) {
	params := map[string]any{"format": "png"}
	if fullPage {
		params["captureBeyondViewport"] = true
	}
	raw, err := s.call(ctx, "Page.captureScreenshot", params)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Data)
}

// PressKey sends one key to the focused element.
func (s *Session) PressKey(ctx context.Context, key string) error {
	code, vk, text := keyInfo(key)
	down := map[string]any{
		"type": "keyDown", "key": code, "code": code, "windowsVirtualKeyCode": vk,
	}
	if text != "" {
		down["text"] = text
		down["type"] = "keyDown"
	}
	if _, err := s.call(ctx, "Input.dispatchKeyEvent", down); err != nil {
		return err
	}
	_, err := s.call(ctx, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": code, "code": code, "windowsVirtualKeyCode": vk,
	})
	return err
}

// InsertText types text into the focused element as if a human had.
func (s *Session) InsertText(ctx context.Context, text string) error {
	_, err := s.call(ctx, "Input.insertText", map[string]any{"text": text})
	return err
}

// Console drains buffered console output.
func (s *Session) Console() []string {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	var out []string
	for _, raw := range c.takeEvents("Runtime.consoleAPICalled") {
		var e struct {
			Type string `json:"type"`
			Args []struct {
				Value       json.RawMessage `json:"value"`
				Description string          `json:"description"`
			} `json:"args"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		parts := make([]string, 0, len(e.Args))
		for _, a := range e.Args {
			if len(a.Value) > 0 {
				var sv string
				if json.Unmarshal(a.Value, &sv) == nil {
					parts = append(parts, sv)
					continue
				}
				parts = append(parts, string(a.Value))
				continue
			}
			parts = append(parts, a.Description)
		}
		out = append(out, e.Type+": "+strings.Join(parts, " "))
	}
	for _, raw := range c.takeEvents("Log.entryAdded") {
		var e struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		out = append(out, e.Entry.Level+": "+e.Entry.Text)
	}
	return out
}

// Requests drains the buffered network log.
func (s *Session) Requests() []string {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	var out []string
	for _, raw := range c.takeEvents("Network.responseReceived") {
		var e struct {
			Response struct {
				URL    string `json:"url"`
				Status int    `json:"status"`
			} `json:"response"`
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		out = append(out, fmt.Sprintf("%d %s %s", e.Response.Status, e.Type, e.Response.URL))
	}
	return out
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// keyInfo maps a friendly key name onto what CDP wants.
func keyInfo(key string) (code string, vk int, text string) {
	switch strings.ToLower(key) {
	case "enter", "return":
		return "Enter", 13, "\r"
	case "tab":
		return "Tab", 9, ""
	case "escape", "esc":
		return "Escape", 27, ""
	case "backspace":
		return "Backspace", 8, ""
	case "delete":
		return "Delete", 46, ""
	case "arrowup", "up":
		return "ArrowUp", 38, ""
	case "arrowdown", "down":
		return "ArrowDown", 40, ""
	case "arrowleft", "left":
		return "ArrowLeft", 37, ""
	case "arrowright", "right":
		return "ArrowRight", 39, ""
	case "pageup":
		return "PageUp", 33, ""
	case "pagedown":
		return "PageDown", 34, ""
	case "home":
		return "Home", 36, ""
	case "end":
		return "End", 35, ""
	case "space":
		return "Space", 32, " "
	}
	// A single character is typed as itself.
	if len([]rune(key)) == 1 {
		r := []rune(key)[0]
		return string(r), int(r), string(r)
	}
	return key, 0, ""
}
