package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// TestEmailFullSolveStress measures the headless Turnstile solve rate. It runs
// only the token step (no API lookup, so it never trips emailosint's rate
// limit) N times and reports how many produced a token.
//
//	ANTARES_STRESS=10 [ANTARES_LIVE_PROXY=first] \
//	  go test ./internal/tools/ -run TestEmailFullSolveStress -v -timeout 1200s
func TestEmailFullSolveStress(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("ANTARES_STRESS"))
	if n <= 0 {
		t.Skip("set ANTARES_STRESS=<count> to run the solve stress test")
	}
	cfg, err := config.Reload()
	if err != nil {
		cfg = config.Default()
	}
	cfg.Tools.Browser.Enabled = true
	cfg.Tools.Browser.Stealth = true

	proxyURL := ""
	if ref := os.Getenv("ANTARES_LIVE_PROXY"); ref != "" {
		if ref == "first" && len(cfg.Proxies.Entries) > 0 {
			ref = cfg.Proxies.Entries[0].ID
		}
		proxyURL = cfg.Proxies.Find(ref)
	}

	ok, fail := 0, 0
	var durs []time.Duration
	for i := 0; i < n; i++ {
		in := Input{
			SessionID: fmt.Sprintf("stress-%d", i),
			Deps:      &Deps{Config: cfg},
			Emit:      func(Progress) {},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		start := timeNow()
		tok, err := emailOSINTToken(ctx, in, proxyURL)
		cancel()
		d := timeNow().Sub(start)
		durs = append(durs, d)
		if err == nil && tok != "" {
			ok++
			t.Logf("run %d/%d: OK in %s (token %d chars)", i+1, n, d.Round(time.Millisecond), len(tok))
		} else {
			fail++
			t.Logf("run %d/%d: FAIL in %s: %v", i+1, n, d.Round(time.Millisecond), err)
		}
	}
	rate := float64(ok) / float64(n) * 100
	t.Logf("=== solve rate: %d/%d = %.0f%% (fail %d) ===", ok, n, rate, fail)
	// Headless Turnstile on this managed sitekey caps around 60–70% per attempt;
	// the tool's caller (the OSINT agent) retries osint_email_full up to 5×, so a
	// ~65% per-attempt rate is >99% effective. Fail below 55% — that would mean
	// something regressed, not just the normal headless variance.
	if rate < 55 {
		t.Errorf("solve rate %.0f%% is unexpectedly low (regression?)", rate)
	}
}

// timeNow wraps time.Now so the stress test stays a normal test (the scripting
// sandbox restriction on Date.now does not apply to Go tests).
func timeNow() time.Time { return time.Now() }
