package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/browser"
	"github.com/enowdev/antares/internal/config"
)

// osint_email_full drives emailosint.org's own investigation engine: it resolves
// an email to registered accounts, rich profile data, data-breach and stealer-log
// exposure, and an AI risk summary. The service gates its API behind a Cloudflare
// Turnstile challenge, so the tool first harvests a fresh Turnstile token with the
// anti-detect browser (real browser solves the challenge), then runs the entire
// lookup over plain HTTP against the /lookup/email-stream SSE endpoint.
//
// This is meant to be the FIRST step of an email investigation: its list of
// confirmed accounts, usernames, and linked identities is the seed the other
// osint_* tools branch from. For authorized investigations.
type osintEmailFullTool struct{}

const (
	// emailosint.org's public site + API. The Turnstile sitekey is the one the
	// site embeds; the token is submitted in the x-captcha-token header.
	emailOSINTSite     = "https://emailosint.org/"
	emailOSINTOrigin   = "https://emailosint.org"
	emailOSINTStream   = "https://api.emailosint.org/lookup/email-stream"
	emailOSINTSitekey  = "0x4AAAAAADFWVaOUWO9IqmDd"
	emailOSINTClientUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"
)

func (osintEmailFullTool) Name() string { return "osint_email_full" }
func (osintEmailFullTool) Description() string {
	return "Deep email investigation via emailosint.org: resolves an email to registered accounts, profile " +
		"data (names, usernames, bios, locations), data-breach and stealer-log exposure, and an AI risk summary. " +
		"Briefly opens the headless anti-detect browser to solve the site's Cloudflare Turnstile (closed right " +
		"after), then runs the whole lookup over HTTP. ALWAYS use this FIRST for an email and use ONLY this tool " +
		"until it succeeds — its accounts and usernames seed everything else. If a proxy is stored it is used " +
		"automatically (no need to pass one). The solve is flaky (~65%/try) and can rate-limit: on ANY error " +
		"(Turnstile/token/HTTP 429/timeout), just call this exact tool again — up to 5 times — before ever " +
		"reaching for another tool. Do not run osint_email/osint_username/etc. until this has failed 5×. For authorized use."
}
func (osintEmailFullTool) Schema() map[string]any {
	return schema(map[string]any{
		"email":           prop("string", "The email address to investigate."),
		"timeout_seconds": propDefault("integer", "Overall budget for the solve + stream.", 90),
		"proxy":           prop("string", "Optional. Force a specific stored proxy by id or label (see list_proxies). Normally leave this empty — the first stored proxy is used automatically, and direct if none is stored."),
	}, "email")
}
func (osintEmailFullTool) RequiresApproval() bool { return false }

func (osintEmailFullTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Email   string `json:"email"`
		Timeout int    `json:"timeout_seconds"`
		Proxy   string `json:"proxy"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	email := strings.TrimSpace(strings.ToLower(args.Email))
	if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return Errorf("%q is not a valid email", email)
	}
	if args.Timeout <= 0 || args.Timeout > 300 {
		args.Timeout = 90
	}
	if in.Deps == nil || in.Deps.Config == nil {
		return Errorf("browser is not available in this runtime")
	}
	cfg := in.Deps.Config
	if !cfg.Tools.Browser.Enabled {
		return Errorf("emailosint.org needs a Turnstile token, which requires the browser — enable tools.browser.enabled")
	}

	// Proxy selection:
	//   - explicit ref (id/label) → use exactly that (error if unknown)
	//   - no ref, but a proxy is stored → auto-use it from the first call, so the
	//     lookup routes through a non-home IP and the agent never sees a 429 that
	//     would tempt it to wander off to other tools
	//   - no ref, no stored proxy → direct
	proxyURL := ""
	if ref := strings.TrimSpace(args.Proxy); ref != "" {
		proxyURL = cfg.Proxies.Find(ref)
		if proxyURL == "" {
			return Errorf("no stored proxy matches %q — check list_proxies", ref)
		}
	} else if p := cfg.Proxies.First(); p != "" {
		proxyURL = p
		in.Emit(Progress{Tool: "osint_email_full", Message: "routing through the stored proxy"})
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
	defer cancel()

	// 1. Harvest a fresh single-use Turnstile token (one bounded attempt). The
	// solve is flaky headless, so the error tells the agent to just call this
	// tool again — up to 5× before trying anything else.
	token, err := emailOSINTToken(ctx, in, proxyURL)
	if err != nil {
		return Errorf("could not obtain a Turnstile token (%v) — the solve is flaky; call osint_email_full again (retry up to 5×) before trying any other tool.", err)
	}

	// 2. Run the whole lookup over HTTP against the SSE endpoint.
	in.Emit(Progress{Tool: "osint_email_full", Message: "querying emailosint.org…"})
	res, err := emailOSINTStreamLookup(ctx, in, email, token, proxyURL)
	if err != nil {
		return Errorf("lookup failed: %v", err)
	}
	return res
}

// emailOSINTToken loads emailosint.org in a throwaway anti-detect browser and
// renders a fresh, single-use Turnstile token for its sitekey.
//
// It injects an explicit-render Turnstile widget rather than scraping the site's
// own hidden field: an explicit widget with the same sitekey and page origin
// yields an equally valid token on a deterministic callback we can poll. The
// stealth build clears Cloudflare's page challenge headlessly, so no visible
// window is needed.
//
// This is ONE bounded attempt — the headless solve is flaky (~80% per try), so
// retrying is the caller's job: the agent re-runs osint_email_full up to 5×
// before falling back to anything else. Keeping each attempt to a single fast
// try makes those agent-level retries quick, and the browser is torn down at the
// end of every call so nothing lingers as a zombie.
//
// The session is dedicated (its own key) and closed as soon as the token is
// harvested — it must never linger as a zombie browser.
func emailOSINTToken(ctx context.Context, in Input, proxyURL string) (token string, err error) {
	in.Emit(Progress{Tool: "osint_email_full", Message: "solving Turnstile challenge…"})
	// Bound the whole attempt. Browser startup + navigate through a residential
	// proxy already costs ~8–15s before the widget can even run, and the solve
	// itself wants a few more — so 40s is the realistic floor. Past that it has
	// stalled and the caller's next retry (fresh browser) is the better bet.
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 40*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
	}
	// Honour the configured headless/headed default (headless by default); the
	// stealth Chromium solves Turnstile without a visible window.
	cfg := *in.Deps.Config
	// Use a throwaway profile, NOT the shared ~/.antares/browser one: a leftover
	// SingletonLock there (from the user's own browsing or a prior run) makes
	// Chrome refuse to open its debug port ("did not open its debugging port in
	// time"). An empty UserDataDir makes Start() mint a fresh temp profile, so
	// this one-shot solve never contends with anything.
	cfg.Tools.Browser.UserDataDir = ""
	// Solve through the same proxy the lookup will use, so the token is minted
	// from the same IP (some challenges bind the token to the requesting IP).
	if proxyURL != "" {
		cfg.Tools.Browser.Proxy = proxyURL
	}
	// A coherent, common fingerprint clears more challenges: give the stealth
	// build a real locale + timezone if the config left them blank, so
	// navigator.language, Accept-Language, and the clock all agree.
	if strings.TrimSpace(cfg.Tools.Browser.Locale) == "" {
		cfg.Tools.Browser.Locale = "en-US"
	}
	if strings.TrimSpace(cfg.Tools.Browser.Timezone) == "" {
		cfg.Tools.Browser.Timezone = "America/New_York"
	}
	key := "emailosint:" + in.SessionID
	s := sessionFor(key, &cfg)
	// Always tear the browser down on the way out — this is a one-shot solve, not
	// a persistent session, so leaving it open would leak a browser process.
	defer func() {
		s.Stop()
		browserSessions.Lock()
		delete(browserSessions.byKey, key)
		browserSessions.Unlock()
	}()
	if !s.Started() {
		if err := s.Start(ctx); err != nil {
			return "", fmt.Errorf("start browser: %w", err)
		}
	}
	if err := s.Navigate(ctx, emailOSINTSite); err != nil {
		return "", fmt.Errorf("navigate: %w", err)
	}
	_ = s.WaitReady(ctx, 15*time.Second)
	// A brief settle before rendering our widget — solving the instant the page
	// loads sometimes misfires — but keep it short to stay within the budget.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(700 * time.Millisecond):
	}

	// Ensure the Turnstile script is present, then render an invisible widget and
	// stash the token on window.__ao_tsToken when the callback fires. The widget
	// is self-healing: an error-callback or timeout auto-resets and re-renders
	// (with a fresh cData "action") a few times, which is what lifts the solve
	// rate on a headless client where the first attempt often gets challenged.
	setup := `(() => {
	  window.__ao_tsToken = ""; window.__ao_tsErr = ""; window.__ao_tsTries = 0;
	  const MAX = 8;
	  // Each attempt gets a BRAND-NEW host element (a stale/reset widget often
	  // never recovers headless); we fully remove the old one and render fresh.
	  const fresh = () => {
	    const h = document.createElement('div');
	    // On-screen with real size so the interactive checkbox is visible AND
	    // clickable (a hidden/off-screen widget can't be clicked, and Turnstile
	    // refuses to run in a zero-size or invisible container).
	    h.style.position = 'fixed'; h.style.left = '8px'; h.style.top = '8px';
	    h.style.width = '300px'; h.style.height = '65px'; h.style.zIndex = '2147483647';
	    document.body.appendChild(h);
	    return h;
	  };
	  const again = (delay) => {
	    if (window.__ao_tsToken || window.__ao_tsTries >= MAX) return;
	    setTimeout(render, delay);
	  };
	  let host = null;
	  const render = () => {
	    try {
	      if (!window.turnstile) { window.__ao_tsErr = "no-turnstile"; return; }
	      window.__ao_tsTries++;
	      if (host) { try { host.remove(); } catch(e){} }
	      host = fresh();
	      window.turnstile.render(host, {
	        sitekey: "` + emailOSINTSitekey + `",
	        retry: "auto", "refresh-expired": "auto",
	        callback: (t) => { window.__ao_tsToken = t; window.__ao_tsErr = ""; },
	        'error-callback': () => { window.__ao_tsErr = "error-callback"; again(1200); },
	        'timeout-callback': () => { window.__ao_tsErr = "timeout-callback"; again(800); },
	      });
	    } catch (e) { window.__ao_tsErr = String(e); again(1500); }
	  };
	  if (window.turnstile) { render(); return "ready"; }
	  const sc = document.createElement('script');
	  sc.src = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
	  sc.onload = render;
	  sc.onerror = () => { window.__ao_tsErr = "script-load"; };
	  document.head.appendChild(sc);
	  return "loading";
	})()`
	if _, err := s.EvalString(ctx, setup); err != nil {
		return "", fmt.Errorf("inject turnstile: %w", err)
	}

	// Poll for the token. Turnstile has two modes: it either solves itself and
	// the token appears, or it renders a checkbox and waits for a real click.
	// Waiting alone hangs forever on the interactive variant, so once the widget
	// is up we click its checkbox — a dispatched mouse event at the iframe's
	// coordinates, which is what the cross-origin widget will accept.
	poll := `(() => JSON.stringify({t: window.__ao_tsToken || "", e: window.__ao_tsErr || "", n: window.__ao_tsTries || 0}))()`
	scriptReloads := 0
	lastClick := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for Turnstile token")
		case <-time.After(400 * time.Millisecond):
		}
		raw, err := s.EvalString(ctx, poll)
		if err != nil {
			continue
		}
		var out struct {
			T, E string
			N    int
		}
		if json.Unmarshal([]byte(raw), &out) != nil {
			continue
		}
		if out.T != "" {
			return out.T, nil
		}
		// Click the checkbox: once shortly after it appears, then re-click every
		// few seconds in case the widget re-rendered.
		if lastClick.IsZero() || time.Since(lastClick) > 3*time.Second {
			if clickTurnstile(ctx, s) {
				lastClick = time.Now()
			}
		}
		// A failed script load through a flaky proxy is transient — re-inject the
		// whole setup a couple of times before treating it as fatal.
		if out.E == "script-load" && scriptReloads < 2 {
			scriptReloads++
			_, _ = s.EvalString(ctx, setup)
			continue
		}
		if out.E == "no-turnstile" || out.E == "script-load" {
			return "", fmt.Errorf("turnstile unavailable (%s)", out.E)
		}
	}
}

// clickTurnstile finds the Cloudflare challenge iframe and clicks its checkbox
// (left side, vertically centred) with a real mouse event. Returns false when
// no visible widget iframe is present yet.
func clickTurnstile(ctx context.Context, s *browser.Session) bool {
	raw, err := s.EvalString(ctx, `(() => {
	  const f = [...document.querySelectorAll("iframe")]
	    .find(f => (f.src || "").includes("challenges.cloudflare.com"));
	  if (!f) return "";
	  const r = f.getBoundingClientRect();
	  if (r.width < 10 || r.height < 10) return "";
	  return JSON.stringify([r.left + 30, r.top + r.height / 2]);
	})()`)
	if err != nil || raw == "" {
		return false
	}
	var xy []float64
	if json.Unmarshal([]byte(raw), &xy) != nil || len(xy) != 2 {
		return false
	}
	return s.ClickXY(ctx, xy[0], xy[1]) == nil
}

// --- SSE lookup --------------------------------------------------------------

// sseAccount is a registered/associated account emailosint reports.
type sseAccount struct {
	Domain string
	Name   string
	Type   string // account | register
}

// emailOSINTStreamLookup POSTs the email to the SSE endpoint with the harvested
// Turnstile token and folds the event stream into a single structured report.
func emailOSINTStreamLookup(ctx context.Context, in Input, email, token, proxyURL string) (Result, error) {
	payload, _ := json.Marshal(map[string]string{"email": email})
	req, err := http.NewRequestWithContext(ctx, "POST", emailOSINTStream, strings.NewReader(string(payload)))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("x-captcha-token", token)
	req.Header.Set("Origin", emailOSINTOrigin)
	req.Header.Set("Referer", emailOSINTSite)
	req.Header.Set("User-Agent", emailOSINTClientUA)

	client := webClient
	if proxyURL != "" {
		pc, err := config.ProxyHTTPClient(proxyURL, 5*time.Minute)
		if err != nil {
			return Result{}, fmt.Errorf("invalid proxy: %w", err)
		}
		client = pc
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return Result{}, fmt.Errorf("HTTP %d — the Turnstile token was rejected (expired or invalid); try again", resp.StatusCode)
	}
	if resp.StatusCode == 429 {
		// A stored proxy is already applied automatically; a 429 through it means
		// that IP is limited too. Either way the answer is the same: retry this
		// exact tool — do NOT fan out to other tools. (A residential proxy usually
		// rotates its exit IP per connection, so the next attempt often clears.)
		if proxyURL != "" {
			return Result{}, fmt.Errorf("HTTP 429 — the proxy IP is rate-limited. Do NOT switch tools: call osint_email_full again (up to 5×); the proxy typically rotates to a fresh IP")
		}
		return Result{}, fmt.Errorf("HTTP 429 — emailosint.org rate-limits by IP. Do NOT proceed to other tools. " +
			"Add a proxy on the Proxies page (it is then used automatically) and call osint_email_full again — up to 5×. " +
			"With no proxy, retry after a few minutes.")
	}
	if resp.StatusCode != 200 {
		return Result{}, fmt.Errorf("HTTP %d from emailosint.org", resp.StatusCode)
	}

	// Accumulators for the report.
	var (
		accounts    []sseAccount           // confirmed accounts + registered sites
		usernames   = map[string]bool{}    // usernames surfaced across modules
		profileURLs = map[string]bool{}     // profile links to pivot into
		otherEmails = map[string]bool{}     // linked emails found
		fields      []string               // flattened human-readable identity fields
		breaches    []string               // data-breach sources
		stealer     []string               // stealer-log lines
		summary     aiSummary              // final AI summary
		total, done int
	)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var event string
	var dataBuf strings.Builder

	flush := func() {
		if event == "" && dataBuf.Len() == 0 {
			return
		}
		data := strings.TrimSpace(dataBuf.String())
		dataBuf.Reset()
		if data == "" {
			event = ""
			return
		}
		switch event {
		case "progress":
			var p struct {
				Progress struct{ Completed, Total int } `json:"progress"`
			}
			if json.Unmarshal([]byte(data), &p) == nil {
				done, total = p.Progress.Completed, p.Progress.Total
				if total > 0 {
					in.Emit(Progress{Tool: "osint_email_full",
						Message: fmt.Sprintf("scanning modules %d/%d", done, total),
						Percent: done * 100 / total})
				}
			}
		case "validator_result":
			var v struct {
				Module     sseModule `json:"module"`
				Registered bool      `json:"registered"`
			}
			if json.Unmarshal([]byte(data), &v) == nil && v.Registered {
				accounts = append(accounts, sseAccount{
					Domain: firstNonBlank(v.Module.Domain, v.Module.Name),
					Name:   firstNonBlank(v.Module.NameFormatted, v.Module.Name),
					Type:   "register",
				})
			}
		case "identifier_result":
			var r sseIdentifier
			if json.Unmarshal([]byte(data), &r) == nil {
				accounts = append(accounts, sseAccount{
					Domain: firstNonBlank(r.Module.Domain, r.Module.Name),
					Name:   firstNonBlank(r.Module.NameFormatted, r.Module.Name),
					Type:   "account",
				})
				for _, f := range r.Data.Fields {
					collectIdentityField(f, r.Module.NameFormatted, &fields, usernames, profileURLs, otherEmails)
				}
			}
		case "data_breaches":
			breaches = append(breaches, parseBreaches(data)...)
		case "stealer_logs", "stealer_enrichment":
			stealer = append(stealer, parseStealer(data)...)
		case "ai_summary":
			_ = json.Unmarshal([]byte(data), &summary)
		}
		event = ""
	}

	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			flush() // an event line begins a new record
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return Result{}, fmt.Errorf("reading stream: %w", err)
	}

	return renderEmailOSINT(email, accounts, usernames, profileURLs, otherEmails,
		fields, breaches, stealer, summary, done, total), nil
}

type sseModule struct {
	Domain        string `json:"domain"`
	Name          string `json:"name"`
	NameFormatted string `json:"name_formatted"`
}

type sseIdentifier struct {
	Data struct {
		Fields []sseField `json:"fields"`
	} `json:"data"`
	Module sseModule `json:"module"`
}

type sseField struct {
	Key   string          `json:"key"`
	Label string          `json:"label"`
	Value json.RawMessage `json:"value"`
	Type  string          `json:"type"`
}

type aiSummary struct {
	Headline   string   `json:"headline"`
	Bullets    []string `json:"bullets"`
	Risk       string   `json:"risk"`
	RiskReason string   `json:"risk_reason"`
}

// collectIdentityField turns one identifier field into a human-readable line and
// harvests pivot leads (usernames, profile URLs, linked emails) from it.
func collectIdentityField(f sseField, module string, fields *[]string,
	usernames, profileURLs, otherEmails map[string]bool) {
	val := scalarString(f.Value)
	if val == "" {
		return
	}
	*fields = append(*fields, fmt.Sprintf("  [%s] %s: %s", module, firstNonBlank(f.Label, f.Key), val))
	switch f.Key {
	case "username":
		usernames[val] = true
	case "profile_url":
		if strings.HasPrefix(val, "http") {
			profileURLs[val] = true
		}
	case "email":
		if strings.Contains(val, "@") {
			otherEmails[val] = true
		}
	}
	if f.Type == "url" && strings.HasPrefix(val, "http") {
		profileURLs[val] = true
	}
}

// scalarString renders a JSON value as a compact scalar; objects/arrays collapse
// to their JSON so nested structures (education history, etc.) stay legible.
func scalarString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		if b {
			return "yes"
		}
		return "no"
	}
	compact := strings.TrimSpace(string(raw))
	if len(compact) > 300 {
		compact = compact[:300] + "…"
	}
	return compact
}

func parseBreaches(data string) []string {
	var out []string
	var v struct {
		Breaches []struct {
			Name   string `json:"name"`
			Title  string `json:"title"`
			Domain string `json:"domain"`
		} `json:"breaches"`
	}
	if json.Unmarshal([]byte(data), &v) == nil {
		for _, b := range v.Breaches {
			out = append(out, firstNonBlank(b.Title, b.Name, b.Domain))
		}
	}
	return out
}

func parseStealer(data string) []string {
	var out []string
	// Stealer payloads vary; surface any domain/url/source-ish strings present.
	var generic map[string]any
	if json.Unmarshal([]byte(data), &generic) == nil {
		for _, k := range []string{"source", "domain", "computer_name", "malware", "date"} {
			if v, ok := generic[k]; ok {
				if s := fmt.Sprint(v); strings.TrimSpace(s) != "" && s != "<nil>" {
					out = append(out, k+": "+s)
				}
			}
		}
	}
	return out
}

func renderEmailOSINT(email string, accounts []sseAccount, usernames, profileURLs,
	otherEmails map[string]bool, fields, breaches, stealer []string, summary aiSummary,
	done, total int) Result {
	var b strings.Builder
	fmt.Fprintf(&b, "Deep email intelligence for %s (emailosint.org, %d/%d modules)\n\n", email, done, total)

	if summary.Headline != "" {
		fmt.Fprintf(&b, "Summary: %s\n", summary.Headline)
		if summary.Risk != "" {
			fmt.Fprintf(&b, "Risk: %s — %s\n", strings.ToUpper(summary.Risk), summary.RiskReason)
		}
		for _, bl := range summary.Bullets {
			fmt.Fprintf(&b, "  • %s\n", bl)
		}
		b.WriteByte('\n')
	}

	// De-duplicate accounts by domain, preferring the richer "account" type.
	seen := map[string]sseAccount{}
	for _, a := range accounts {
		if a.Domain == "" {
			continue
		}
		if prev, ok := seen[a.Domain]; !ok || (prev.Type != "account" && a.Type == "account") {
			seen[a.Domain] = a
		}
	}
	if len(seen) > 0 {
		fmt.Fprintf(&b, "Accounts found (%d):\n", len(seen))
		for _, a := range sortedAccounts(seen) {
			marker := "registered"
			if a.Type == "account" {
				marker = "confirmed"
			}
			fmt.Fprintf(&b, "  - %-24s [%s]\n", firstNonBlank(a.Name, a.Domain), marker)
		}
		b.WriteByte('\n')
	} else {
		b.WriteString("Accounts found: none\n\n")
	}

	if len(fields) > 0 {
		b.WriteString("Profile data:\n")
		for _, f := range fields {
			b.WriteString(f)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	writeSet(&b, "Usernames (pivot on these)", usernames)
	writeSet(&b, "Profile URLs (scrape/pivot)", profileURLs)
	writeSet(&b, "Linked emails (branch into these)", otherEmails)

	if len(breaches) > 0 {
		fmt.Fprintf(&b, "Data breaches (%d):\n", len(breaches))
		for _, x := range uniqueStrings(breaches) {
			fmt.Fprintf(&b, "  - %s\n", x)
		}
		b.WriteByte('\n')
	}
	if len(stealer) > 0 {
		fmt.Fprintf(&b, "Stealer-log exposure (%d):\n", len(stealer))
		for _, x := range uniqueStrings(stealer) {
			fmt.Fprintf(&b, "  - %s\n", x)
		}
		b.WriteByte('\n')
	}

	b.WriteString("Next: pivot on the usernames and profile URLs above with osint_username, " +
		"osint_pivot, and osint_github; branch into any linked emails with another osint_email_full.")

	meta := map[string]any{
		"accounts":  len(seen),
		"usernames": len(usernames),
		"breaches":  len(uniqueStrings(breaches)),
	}
	return Result{Content: b.String(), Meta: meta}
}

func sortedAccounts(m map[string]sseAccount) []sseAccount {
	out := make([]sseAccount, 0, len(m))
	for _, a := range m {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Type == "account") != (out[j].Type == "account") {
			return out[i].Type == "account" // confirmed first
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}

func writeSet(b *strings.Builder, label string, set map[string]bool) {
	if len(set) == 0 {
		return
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "%s (%d):\n", label, len(keys))
	for _, k := range keys {
		fmt.Fprintf(b, "  - %s\n", k)
	}
	b.WriteByte('\n')
}
