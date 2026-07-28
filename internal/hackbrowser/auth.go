// Auth: session save/load, auto-login, manual-login wait, 2FA detect.
//
// The TypeScript original ships an elaborate in-page tactical-HUD button
// for manual login confirmation; v1 of this port uses a simpler terminal
// prompt (the user presses Enter in the antares session to confirm each
// login). The elaborate button can be added later — it's pure UI on top
// of the same coordination primitive (a callback that resolves a promise).
//
// Three responsibilities:
//   1. SaveSession / LoadSession — persist cookies across runs
//   2. AutoLogin — fill username/password and submit
//   3. WaitForManualLogin — pause for the user to log in by hand
//   4. Handle2FA — detect an OTP prompt and pause for the code

package hackbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/browser"
)

var authLog = Log.Create("hackbrowser:auth")

// ============================================================
// Session persistence (cookies)
// ============================================================

// SessionFile is the JSON shape persisted to disk by SaveSession and read
// back by LoadSession. It is intentionally a thin wrapper around a slice
// of browser.Cookie — local-storage and IndexedDB persistence would
// require CDP Storage domain calls and add complexity without much real
// benefit (most authenticated SPAs rely on cookies, not localStorage).
type SessionFile struct {
	Cookies []browser.Cookie `json:"cookies"`
	SavedAt time.Time        `json:"saved_at"`
}

// SaveSession writes the browser's current cookies for one or more URLs
// to filePath. Existing file is overwritten.
func SaveSession(ctx context.Context, sess *browser.Session, filePath string, urls []string) error {
	cookies, err := sess.Cookies(ctx, urls...)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	payload := SessionFile{Cookies: cookies, SavedAt: time.Now()}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	authLog.Info("session saved", F("file", filePath), F("cookies", len(cookies)))
	return nil
}

// LoadSession reads cookies from filePath and installs them into the
// browser. Returns (true, nil) on success; (false, nil) when the file
// does not exist (first-run case, not an error).
func LoadSession(ctx context.Context, sess *browser.Session, filePath string) (bool, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			authLog.Info("no session file found — starting anonymous", F("file", filePath))
			return false, nil
		}
		return false, fmt.Errorf("load session: %w", err)
	}
	var payload SessionFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return false, fmt.Errorf("load session parse: %w", err)
	}
	if err := sess.SetCookies(ctx, payload.Cookies); err != nil {
		return false, fmt.Errorf("load session install: %w", err)
	}
	authLog.Info("session loaded", F("file", filePath), F("cookies", len(payload.Cookies)))
	return true, nil
}

// ============================================================
// Auto-login
// ============================================================

// AutoLogin fills a username and password into the page's login form and
// submits it. Selector fallbacks cover the common cases (input[type=email],
// input[name=user], etc.); callers can override with explicit selectors
// when the form is non-standard.
//
// On failure (form not found, timeout), the caller should fall back to
// WaitForManualLogin — auto-login is best-effort, not authoritative.
func AutoLogin(ctx context.Context, sess *browser.Session, creds LoginCredentials) error {
	userSel := creds.UsernameSelector
	if userSel == "" {
		// Broad fallback chain — try the most-common shapes first.
		userSel = `input[type="email"], input[type="text"], input[name*="user" i], input[name*="email" i], input[id*="user" i], input[id*="email" i]`
	}
	passSel := creds.PasswordSelector
	if passSel == "" {
		passSel = `input[type="password"]`
	}

	// Wait for the username field to appear.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sess.WaitForSelector(waitCtx, firstSelector(userSel), 5*time.Second); err != nil {
		return fmt.Errorf("login form not visible: %w", err)
	}

	// Fill each field via the same JS path the executor uses. FillSelector
	// takes a single selector; we resolve the comma-separated list to the
	// first match by asking the page.
	userResolved, err := resolveFirstJSX(ctx, sess, userSel)
	if err != nil || userResolved == "" {
		return errors.New("could not resolve username input")
	}
	if out, err := sess.FillSelector(ctx, userResolved, creds.Username); err != nil || out != "ok" {
		return fmt.Errorf("fill username: %v (%s)", err, out)
	}
	passResolved, err := resolveFirstJSX(ctx, sess, passSel)
	if err != nil || passResolved == "" {
		return errors.New("could not resolve password input")
	}
	if out, err := sess.FillSelector(ctx, passResolved, creds.Password); err != nil || out != "ok" {
		return fmt.Errorf("fill password: %v (%s)", err, out)
	}

	// Submit. Prefer an explicit submit button; fall back to pressing Enter
	// on the password field (works for most SPA login forms).
	clicked, _ := sess.EvalString(ctx, `(() => {
  const btn = document.querySelector('button[type="submit"], input[type="submit"], button:not([type])');
  if (!btn) return '';
  btn.click();
  return 'clicked';
})()`)
	if clicked != "clicked" {
		_ = sess.PressKey(ctx, "Enter")
	}
	_ = sess.WaitReady(ctx, 5*time.Second)
	authLog.Info("auto-login attempted", F("username", creds.Username))

	// 2FA / OTP detection. Best-effort — failure to detect does not fail
	// the login; the caller may still see an authenticated session.
	_ = Handle2FA(ctx, sess)
	return nil
}

// firstSelector returns the first selector in a comma-separated list. Used
// to convert the broad fallback chain into one selector for WaitForSelector
// (which takes one selector).
func firstSelector(list string) string {
	for _, s := range strings.Split(list, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return list
}

// resolveFirstJSX is the public wrapper: it asks the page to resolve a
// comma-separated selector list to the first selector that matches
// something, returning that selector as a string (ready for FillSelector).
func resolveFirstJSX(ctx context.Context, sess *browser.Session, list string) (string, error) {
	js := fmt.Sprintf(`(() => { const list = %s.split(","); for (const sel of list) { const s = sel.trim(); if (!s) continue; try { if (document.querySelector(s)) return s; } catch(e) {} } return ""; })()`, jsonStr(list))
	return sess.EvalString(ctx, js)
}

// ============================================================
// 2FA / OTP detection
// ============================================================

var mfaSelectors = []string{
	`input[name*="otp" i]`,
	`input[name*="mfa" i]`,
	`input[name*="totp" i]`,
	`input[name*="2fa" i]`,
	`input[name*="verification" i]`,
	`input[placeholder*="verification code" i]`,
	`input[placeholder*="auth code" i]`,
	`input[placeholder*="one-time" i]`,
	`input[placeholder*="otp" i]`,
	`input[autocomplete="one-time-code"]`,
}

// Handle2FA detects a visible OTP/MFA input and prompts the user for a
// code via the provided PromptFunc. If no MFA field is visible, returns
// false immediately.
//
// In v1 the prompt is terminal-based. The caller passes a function that
// knows how to ask the user (the agent tool surfaces this as an ask_user
// step).
type PromptFunc func(ctx context.Context, prompt string) (string, error)

// Handle2FA returns true when an OTP field was found and filled.
func Handle2FA(ctx context.Context, sess *browser.Session) bool {
	return Handle2FAWithPrompt(ctx, sess, nil)
}

// Handle2FAWithPrompt is the testable form. When prompt is nil and an OTP
// field IS found, the field is left empty and the function returns true —
// the caller then knows to ask the user out-of-band.
func Handle2FAWithPrompt(ctx context.Context, sess *browser.Session, prompt PromptFunc) bool {
	for _, sel := range mfaSelectors {
		// WaitForSelector is one-selector only; pick the first via JS.
		foundJS := fmt.Sprintf(`!!document.querySelector(%s)`, jsonStr(sel))
		raw, err := sess.Eval(ctx, foundJS)
		if err != nil {
			continue
		}
		var found bool
		_ = json.Unmarshal(raw, &found)
		if !found {
			continue
		}
		authLog.Info("2FA field detected", F("selector", sel))
		if prompt == nil {
			return true
		}
		code, err := prompt(ctx, "Enter your 2FA code:")
		if err != nil || strings.TrimSpace(code) == "" {
			return true
		}
		resolved, _ := sess.EvalString(ctx, fmt.Sprintf(`(document.querySelector(%s)?.value = %s, 'ok')`, jsonStr(sel), jsonStr(strings.TrimSpace(code))))
		if resolved != "ok" {
			continue
		}
		// Try to submit the form.
		_, _ = sess.EvalString(ctx, `(() => {
  const form = document.querySelector("form");
  if (!form) return '';
  const btn = form.querySelector('button[type="submit"], input[type="submit"], button:not([type])');
  if (btn) { btn.click(); return 'clicked'; }
  return '';
})()`)
		_ = sess.WaitReady(ctx, 5*time.Second)
		return true
	}
	return false
}

// ============================================================
// Manual login wait
// ============================================================

// ManualLoginRequest is what an auth flow returns when it needs the user
// to log in by hand. The agent surfaces this to the user (terminal prompt
// or ask_user tool) and calls ConfirmManualLogin once the user is ready.
type ManualLoginRequest struct {
	Label  string // "admin", "user", ... — empty for single-credential
	URL    string // page the user should be on
	Step   int    // 1-based step in a multi-credential sequence
	Total  int    // total steps in the sequence
}

// ConfirmFunc resumes a paused manual-login wait.
type ConfirmFunc func()

// WaitForManualLogin blocks until the caller signals that the user has
// finished logging in. v1 of this port has no in-page button UI — the
// caller (agent loop) asks the user out-of-band, then calls the returned
// ConfirmFunc.
//
// This is structured so the agent loop can present the request any way it
// likes (terminal, web dashboard, slash command) without the auth package
// needing to know about any of those surfaces.
func WaitForManualLogin(ctx context.Context, sess *browser.Session, req ManualLoginRequest) (ConfirmFunc, <-chan struct{}) {
	done := make(chan struct{})
	confirm := func() {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	authLog.Info("waiting for manual login",
		F("label", req.Label),
		F("step", req.Step),
		F("total", req.Total),
		F("url", req.URL))
	return confirm, done
}
