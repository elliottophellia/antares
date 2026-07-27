package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// snapshotJS walks the page and returns the elements a person could act on,
// each with a stable reference. A screenshot would need a vision model and
// pixel coordinates; this gives a text model something it can name and click.
//
// The refs live on the page under a single global so a later click resolves
// against the very element that was described, not a re-query that may match
// something else after the DOM moved.
const snapshotJS = `(() => {
  const MAX = %d;
  const out = [];
  window.__antaresRefs = [];

  const visible = (el) => {
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) return false;
    const st = getComputedStyle(el);
    if (st.visibility === 'hidden' || st.display === 'none' || st.opacity === '0') return false;
    // Below or above the document is fine; entirely off to the side is not.
    return r.right > 0 && r.left < (window.innerWidth || 0) + 2000;
  };

  const label = (el) => {
    const pick = (s) => (s || '').replace(/\s+/g, ' ').trim();
    return pick(
      el.getAttribute('aria-label') ||
      el.getAttribute('placeholder') ||
      el.getAttribute('title') ||
      (el.labels && el.labels[0] && el.labels[0].innerText) ||
      el.value ||
      el.innerText ||
      el.getAttribute('alt') ||
      el.getAttribute('name') ||
      ''
    ).slice(0, 120);
  };

  const role = (el) => {
    const explicit = el.getAttribute('role');
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    if (tag === 'a') return 'link';
    if (tag === 'button') return 'button';
    if (tag === 'select') return 'select';
    if (tag === 'textarea') return 'textbox';
    if (tag === 'input') {
      const t = (el.type || 'text').toLowerCase();
      if (t === 'checkbox' || t === 'radio') return t;
      if (t === 'submit' || t === 'button') return 'button';
      return 'textbox';
    }
    if (/^h[1-6]$/.test(tag)) return 'heading';
    return tag;
  };

  const SELECTOR = [
    'a[href]', 'button', 'input:not([type=hidden])', 'select', 'textarea',
    '[role=button]', '[role=link]', '[role=tab]', '[role=menuitem]',
    '[role=checkbox]', '[role=radio]', '[role=option]', '[role=switch]',
    '[contenteditable=true]', '[onclick]', 'summary',
    'h1', 'h2', 'h3',
  ].join(',');

  for (const el of document.querySelectorAll(SELECTOR)) {
    if (out.length >= MAX) break;
    if (el.disabled) continue;
    if (!visible(el)) continue;
    const text = label(el);
    const r = role(el);
    // A nameless heading or link is noise the model cannot use.
    if (!text && r !== 'checkbox' && r !== 'radio' && r !== 'textbox') continue;

    const idx = window.__antaresRefs.push(el) - 1;
    const bits = ['e' + (idx + 1), r];
    if (text) bits.push(JSON.stringify(text));
    if (r === 'textbox' && el.value) bits.push('value=' + JSON.stringify(String(el.value).slice(0, 60)));
    if (r === 'checkbox' || r === 'radio') bits.push(el.checked ? 'checked' : 'unchecked');
    if (el.tagName.toLowerCase() === 'a' && el.href) {
      try {
        const u = new URL(el.href);
        bits.push('→ ' + (u.origin === location.origin ? u.pathname : u.href).slice(0, 80));
      } catch (_) { /* opaque href */ }
    }
    out.push(bits.join(' '));
  }
  return out.join('\n');
})()`

// Snapshot returns a compact, referenced view of what is on the page.
func (s *Session) Snapshot(ctx context.Context, max int) (string, error) {
	if max <= 0 {
		max = 120
	}
	return s.EvalString(ctx, fmt.Sprintf(snapshotJS, max))
}

// refExpr turns "e12" into the JavaScript that reaches that element.
func refExpr(ref string) (string, error) {
	r := strings.TrimSpace(ref)
	if !strings.HasPrefix(r, "e") {
		return "", fmt.Errorf("%q is not an element reference — take a snapshot first, then use one of its e-numbers", ref)
	}
	var n int
	if _, err := fmt.Sscanf(r[1:], "%d", &n); err != nil || n < 1 {
		return "", fmt.Errorf("%q is not an element reference", ref)
	}
	return fmt.Sprintf("(window.__antaresRefs||[])[%d]", n-1), nil
}

// Click activates a referenced element, scrolling it into view first.
func (s *Session) Click(ctx context.Context, ref string) (string, error) {
	expr, err := refExpr(ref)
	if err != nil {
		return "", err
	}
	// Clicking through the element itself works with React handlers and does
	// not depend on the element still being where it was painted.
	js := fmt.Sprintf(`(() => {
      const el = %s;
      if (!el) return 'gone';
      el.scrollIntoView({block:'center', inline:'center'});
      el.click();
      return 'ok';
    })()`, expr)
	out, err := s.EvalString(ctx, js)
	if err != nil {
		return "", err
	}
	if out == "gone" {
		return "", fmt.Errorf("%s is no longer on the page — take a fresh snapshot", ref)
	}
	_ = s.WaitReady(ctx, 5*time.Second)
	return "clicked " + ref, nil
}

// Type focuses a referenced field and enters text into it.
func (s *Session) Type(ctx context.Context, ref, text string, submit bool) (string, error) {
	expr, err := refExpr(ref)
	if err != nil {
		return "", err
	}
	js := fmt.Sprintf(`(() => {
      const el = %s;
      if (!el) return 'gone';
      el.scrollIntoView({block:'center'});
      el.focus();
      if ('value' in el) {
        // Setting through the prototype setter is what React listens for.
        const proto = el instanceof HTMLTextAreaElement
          ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
        const setter = Object.getOwnPropertyDescriptor(proto, 'value');
        if (setter && setter.set) setter.set.call(el, ''); else el.value = '';
        el.dispatchEvent(new Event('input', {bubbles:true}));
      } else if (el.isContentEditable) {
        el.textContent = '';
      }
      return 'ok';
    })()`, expr)
	out, err := s.EvalString(ctx, js)
	if err != nil {
		return "", err
	}
	if out == "gone" {
		return "", fmt.Errorf("%s is no longer on the page — take a fresh snapshot", ref)
	}
	if err := s.InsertText(ctx, text); err != nil {
		return "", err
	}
	if submit {
		if err := s.PressKey(ctx, "Enter"); err != nil {
			return "", err
		}
		_ = s.WaitReady(ctx, 10*time.Second)
	}
	return "typed into " + ref, nil
}

// Select picks an option in a referenced <select> by value or visible label.
func (s *Session) Select(ctx context.Context, ref, value string) (string, error) {
	expr, err := refExpr(ref)
	if err != nil {
		return "", err
	}
	js := fmt.Sprintf(`(() => {
      const el = %s;
      if (!el) return 'gone';
      const want = %s;
      const opt = [...el.options].find(o => o.value === want || o.text.trim() === want);
      if (!opt) return 'missing';
      el.value = opt.value;
      el.dispatchEvent(new Event('change', {bubbles:true}));
      return 'ok';
    })()`, expr, jsonString(value))
	out, err := s.EvalString(ctx, js)
	if err != nil {
		return "", err
	}
	switch out {
	case "gone":
		return "", fmt.Errorf("%s is no longer on the page", ref)
	case "missing":
		return "", fmt.Errorf("no option matching %q", value)
	}
	return "selected " + value, nil
}

// Text extracts the readable text of the page, or of one referenced element.
func (s *Session) Text(ctx context.Context, ref string, max int) (string, error) {
	target := "document.body"
	if ref != "" {
		expr, err := refExpr(ref)
		if err != nil {
			return "", err
		}
		target = expr
	}
	if max <= 0 {
		max = 8000
	}
	js := fmt.Sprintf(`(() => {
      const el = %s;
      if (!el) return '';
      return (el.innerText || '').replace(/\n{3,}/g, '\n\n').slice(0, %d);
    })()`, target, max)
	return s.EvalString(ctx, js)
}

// Scroll moves the page or a referenced element.
func (s *Session) Scroll(ctx context.Context, direction string, amount int) (string, error) {
	if amount <= 0 {
		amount = 600
	}
	dy, dx := 0, 0
	switch strings.ToLower(direction) {
	case "up":
		dy = -amount
	case "left":
		dx = -amount
	case "right":
		dx = amount
	case "top":
		if _, err := s.Eval(ctx, "window.scrollTo(0,0)"); err != nil {
			return "", err
		}
		return "scrolled to the top", nil
	case "bottom":
		if _, err := s.Eval(ctx, "window.scrollTo(0, document.body.scrollHeight)"); err != nil {
			return "", err
		}
		return "scrolled to the bottom", nil
	default:
		dy = amount
	}
	if _, err := s.Eval(ctx, fmt.Sprintf("window.scrollBy(%d,%d)", dx, dy)); err != nil {
		return "", err
	}
	return fmt.Sprintf("scrolled %s", strings.ToLower(direction)), nil
}

// WaitFor polls until text appears on the page or the budget runs out.
func (s *Session) WaitFor(ctx context.Context, text string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)
	js := fmt.Sprintf(`(document.body.innerText || '').includes(%s)`, jsonString(text))
	for time.Now().Before(deadline) {
		v, err := s.Eval(ctx, js)
		if err == nil {
			var found bool
			if json.Unmarshal(v, &found) == nil && found {
				return fmt.Sprintf("%q appeared", text), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("%q did not appear within %s", text, timeout)
}

// Back goes to the previous page in history.
func (s *Session) Back(ctx context.Context) error {
	if _, err := s.Eval(ctx, "history.back()"); err != nil {
		return err
	}
	return s.WaitReady(ctx, 10*time.Second)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
