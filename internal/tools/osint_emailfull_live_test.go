package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// TestEmailFullLive exercises the real Turnstile solve + SSE lookup end to end.
// It is opt-in: set ANTARES_LIVE_EMAILOSINT=<email> to run. It needs a working
// browser (Chrome/Chromium) and network access.
//
//	ANTARES_LIVE_EMAILOSINT=someone@example.com \
//	  go test ./internal/tools/ -run TestEmailFullLive -v -timeout 300s
func TestEmailFullLive(t *testing.T) {
	email := os.Getenv("ANTARES_LIVE_EMAILOSINT")
	if email == "" {
		t.Skip("set ANTARES_LIVE_EMAILOSINT=<email> to run this live test")
	}
	// Load the real saved config so the active proxy (if any) is honoured; fall
	// back to defaults if there is none on disk.
	cfg, err := config.Reload()
	if err != nil {
		cfg = config.Default()
	}
	cfg.Tools.Browser.Enabled = true
	cfg.Tools.Browser.Stealth = true
	// Leave Headed at the default (false) on purpose: the tool must force a headed
	// session for the Turnstile step itself. This proves that override works.
	if p := cfg.ActiveProxyURL(); p != "" {
		t.Logf("routing through active proxy")
	}

	args, _ := json.Marshal(map[string]any{"email": email, "timeout_seconds": 240})
	in := Input{
		SessionID: "live-emailfull-test",
		Args:      args,
		Deps:      &Deps{Config: cfg},
		Emit:      func(p Progress) { t.Logf("progress: %s", p.Message) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 280*time.Second)
	defer cancel()

	res := osintEmailFullTool{}.Execute(ctx, in)
	if res.IsError {
		t.Fatalf("tool returned error: %s", res.Content)
	}
	t.Logf("\n%s", res.Content)
	t.Logf("meta: %+v", res.Meta)
}
