package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Selector-based interaction primitives.
//
// The browser tool drives the page through element references ("e12" →
// window.__antaresRefs[11]) because that pins the action to the very DOM
// node the snapshot described — robust against re-renders that move things
// around. Hackbrowser (and similar autonomous crawlers) produce CSS
// selectors from their own scanner and want to act on those directly
// without round-tripping through the refs table, so these methods take a
// raw selector string instead.

// ClickSelector scrolls the element matching sel into view and clicks it
// through the DOM node — works with React-style event delegation that
// ignores synthetic MouseEvents.
//
// Returns one of:
//   - "ok"      : the click was dispatched
//   - "gone"    : no element matched sel
//   - "hidden"  : the element is display:none / visibility:hidden / opacity:0
//
// A click is fire-and-forget at the DOM level; if it triggers navigation
// or async work, wait for the document to settle separately.
func (s *Session) ClickSelector(ctx context.Context, sel string) (string, error) {
	js := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s);
  if (!el) return 'gone';
  const r = el.getBoundingClientRect();
  const st = getComputedStyle(el);
  if (st.display === 'none' || st.visibility === 'hidden' || st.opacity === '0') return 'hidden';
  if (r.width < 1 || r.height < 1) return 'hidden';
  el.scrollIntoView({block:'center', inline:'center'});
  el.click();
  return 'ok';
})()`, jsString(sel))
	out, err := s.EvalString(ctx, js)
	if err != nil {
		return "", err
	}
	return out, nil
}

// FillSelector sets the value of an input matched by sel, dispatching
// input/change events the way React/Vue expect (via the prototype setter
// on HTMLInputElement/HTMLTextAreaElement). Clears the field first.
//
// Returns "ok", "gone" (selector matched nothing), or "readonly" (the
// field refuses writes — typical of type=hidden or explicitly readonly).
func (s *Session) FillSelector(ctx context.Context, sel, value string) (string, error) {
	js := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s);
  if (!el) return 'gone';
  if (el.readOnly || el.disabled) return 'readonly';
  el.scrollIntoView({block:'center'});
  el.focus();
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, 'value');
  if (setter && setter.set) setter.set.call(el, %s); else el.value = %s;
  el.dispatchEvent(new Event('input', {bubbles:true}));
  el.dispatchEvent(new Event('change', {bubbles:true}));
  return 'ok';
})()`, jsString(sel), jsString(value), jsString(value))
	return s.EvalString(ctx, js)
}

// PressSelector waits for sel to appear (up to timeout), then triggers a
// keydown+keyup on it. Useful for submit-button-like elements that listen
// for keyboard events rather than click.
func (s *Session) PressSelector(ctx context.Context, sel, key string, timeout time.Duration) error {
	if err := s.WaitForSelector(ctx, sel, timeout); err != nil {
		return err
	}
	// Focus the element first so the key event lands on it.
	_, _ = s.EvalString(ctx, fmt.Sprintf(`(() => { const el = document.querySelector(%s); if (el) el.focus(); })()`, jsString(sel)))
	return s.PressKey(ctx, key)
}

// WaitForSelector polls until sel matches something in the DOM, or the
// timeout elapses. Returns nil when found, an error otherwise.
func (s *Session) WaitForSelector(ctx context.Context, sel string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	js := fmt.Sprintf(`(() => { return !!document.querySelector(%s); })()`, jsString(sel))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		v, err := s.Eval(ctx, js)
		if err == nil {
			var found bool
			if json.Unmarshal(v, &found) == nil && found {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return errors.New("timeout waiting for " + sel)
}

// ============================================================
// Cookie management (CDP Network domain)
// ============================================================

// Cookie is one browser cookie, in the shape CDP returns/expects.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"` // CDP uses seconds-since-epoch as float
	Size     int    `json:"size,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	SameSite string `json:"sameSite,omitempty"`
	Session  bool   `json:"session,omitempty"`
}

// Cookies returns every cookie for the URLs given (or all cookies if none
// are passed). The URLs let CDP pick up partitioned/host-only cookies that
// a bare getCookies would miss.
func (s *Session) Cookies(ctx context.Context, urls ...string) ([]Cookie, error) {
	params := map[string]any{}
	if len(urls) > 0 {
		params["urls"] = urls
	}
	raw, err := s.call(ctx, "Network.getCookies", params)
	if err != nil {
		return nil, err
	}
	var out struct {
		Cookies []Cookie `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Cookies, nil
}

// SetCookies replaces the cookie jar with the given cookies. Existing
// cookies with the same (name, domain, path) tuple are overwritten.
func (s *Session) SetCookies(ctx context.Context, cookies []Cookie) error {
	if _, err := s.call(ctx, "Network.clearBrowserCookies", nil); err != nil {
		return err
	}
	if len(cookies) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(cookies))
	for _, c := range cookies {
		item := map[string]any{
			"name":   c.Name,
			"value":  c.Value,
			"domain": c.Domain,
			"path":   c.Path,
		}
		if c.Expires > 0 {
			item["expires"] = c.Expires
		}
		if c.HTTPOnly {
			item["httpOnly"] = true
		}
		if c.Secure {
			item["secure"] = true
		}
		if c.SameSite != "" {
			item["sameSite"] = c.SameSite
		}
		items = append(items, item)
	}
	_, err := s.call(ctx, "Network.setCookies", map[string]any{"cookies": items})
	return err
}

// ============================================================
// Eval helpers
// ============================================================

// EvalFunc runs `(function(...args){ body })(arg1, arg2, ...)` — for
// callers that prefer passing arguments out-of-line rather than
// interpolating them into the body string. Each arg is JSON-encoded.
func (s *Session) EvalFunc(ctx context.Context, body string, args ...any) (json.RawMessage, error) {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		raw, err := json.Marshal(a)
		if err != nil {
			return nil, fmt.Errorf("eval arg: %w", err)
		}
		parts = append(parts, string(raw))
	}
	expr := "(function(" + placeholderList(len(args)) + "){" + body + "})(" +
		joinComma(parts) + ")"
	return s.Eval(ctx, expr)
}

// jsString quotes s as a JavaScript string literal. Reuse json encoding —
// JSON string syntax is a strict subset of JS string literal syntax for
// our purposes (no exotic escapes needed, no template literals).
func jsString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func placeholderList(n int) string {
	if n <= 0 {
		return ""
	}
	out := "_0"
	for i := 1; i < n; i++ {
		out += fmt.Sprintf(",_%d", i)
	}
	return out
}

func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "," + p
	}
	return out
}
