package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// solveCaptchaTool loads a page in antares' anti-detect browser and harvests the
// replayable CAPTCHA token once the challenge passes — reCAPTCHA v2/v3, hCaptcha,
// and Cloudflare Turnstile. The stealth browser clears many invisible challenges
// on its own; the harvested token plus cookies can then be replayed against the
// origin. For authorized testing of properties you are permitted to assess.
type solveCaptchaTool struct{}

func (solveCaptchaTool) Name() string { return "solve_captcha" }
func (solveCaptchaTool) Description() string {
	return "Solve a CAPTCHA on a page by loading it in the anti-detect browser and harvesting the replayable " +
		"token (reCAPTCHA v2/v3, hCaptcha, Cloudflare Turnstile), along with the session cookies. For authorized " +
		"testing of your own properties. Returns the token and cookies to replay against the origin."
}
func (solveCaptchaTool) Schema() map[string]any {
	return schema(map[string]any{
		"url":             prop("string", "The page URL that presents the CAPTCHA."),
		"timeout_seconds": propDefault("integer", "How long to wait for a token to appear.", 60),
	}, "url")
}
func (solveCaptchaTool) RequiresApproval() bool { return true }

// tokenProbe returns the first populated CAPTCHA token field on the page, as a
// JSON string {"type":..,"token":..}.
const tokenProbe = `(() => {
  const fields = [["recaptcha","g-recaptcha-response"],["hcaptcha","h-captcha-response"],["turnstile","cf-turnstile-response"]];
  for (const [type, name] of fields) {
    let el = document.querySelector('[name="'+name+'"]') || document.getElementById(name);
    if (el && el.value && el.value.length > 20) return JSON.stringify({type: type, token: el.value});
    // Turnstile sometimes stores the token on an input inside the widget.
    let inp = document.querySelector('input[name="'+name+'"]');
    if (inp && inp.value && inp.value.length > 20) return JSON.stringify({type: type, token: inp.value});
  }
  return JSON.stringify({type:"", token:""});
})()`

func (solveCaptchaTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		URL     string `json:"url"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	u := strings.TrimSpace(args.URL)
	if u == "" {
		return Errorf("url is required")
	}
	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	if args.Timeout <= 0 || args.Timeout > 240 {
		args.Timeout = 60
	}
	if in.Deps == nil || in.Deps.Config == nil {
		return Errorf("browser is not available in this runtime")
	}
	cfg := in.Deps.Config
	if !cfg.Tools.Browser.Enabled {
		return Errorf("the browser is switched off (tools.browser.enabled = false)")
	}

	s := sessionFor("captcha:"+in.SessionID, cfg)
	if !s.Started() {
		if err := s.Start(ctx); err != nil {
			return Errorf("start browser: %v", err)
		}
	}
	if err := s.Navigate(ctx, u); err != nil {
		return Errorf("navigate: %v", err)
	}
	_ = s.WaitReady(ctx, 20*time.Second)

	in.Emit(Progress{Tool: "solve_captcha", Message: "waiting for the challenge to pass…"})
	deadline := time.Now().Add(time.Duration(args.Timeout) * time.Second)
	for time.Now().Before(deadline) {
		raw, err := s.EvalString(ctx, tokenProbe)
		if err == nil && raw != "" {
			var out struct{ Type, Token string }
			if json.Unmarshal([]byte(raw), &out) == nil && out.Token != "" {
				cookies := ""
				if cs, err := s.Cookies(ctx, u); err == nil {
					parts := make([]string, 0, len(cs))
					for _, c := range cs {
						parts = append(parts, c.Name+"="+c.Value)
					}
					cookies = strings.Join(parts, "; ")
				}
				var b strings.Builder
				fmt.Fprintf(&b, "CAPTCHA solved (%s).\n\n", firstNonBlank(out.Type, "unknown"))
				fmt.Fprintf(&b, "Token:\n%s\n", out.Token)
				if cookies != "" {
					fmt.Fprintf(&b, "\nCookies:\n%s\n", cookies)
				}
				b.WriteString("\nReplay the token in the origin's expected field (and cookies) within its validity window.")
				return Result{Content: b.String(), Meta: map[string]any{"type": out.Type, "token": out.Token}}
			}
		}
		select {
		case <-ctx.Done():
			return Errorf("cancelled")
		case <-time.After(2 * time.Second):
		}
	}
	return Errorf("no CAPTCHA token appeared within %ds — the challenge may need interactive solving, or the page has no supported CAPTCHA.", args.Timeout)
}
