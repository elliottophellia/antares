package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sardanioss/httpcloak"
)

func TestValidPresetFallsBack(t *testing.T) {
	if got := validPreset(""); got != defaultHTTPPreset {
		t.Fatalf("empty preset should default to %q, got %q", defaultHTTPPreset, got)
	}
	if got := validPreset("not-a-real-preset"); got != defaultHTTPPreset {
		t.Fatalf("unknown preset should fall back, got %q", got)
	}
	// A preset the library actually publishes must survive unchanged.
	presets := httpcloak.Presets()
	if len(presets) == 0 {
		t.Fatal("httpcloak reports no presets")
	}
	if got := validPreset(presets[0]); got != presets[0] {
		t.Fatalf("known preset %q was altered to %q", presets[0], got)
	}
}

func TestHeaderLookupCaseInsensitive(t *testing.T) {
	h := map[string]string{"Content-Type": "application/json"}
	if v, ok := headerLookup(h, "content-type"); !ok || v != "application/json" {
		t.Fatalf("case-insensitive lookup failed: %q %v", v, ok)
	}
	if _, ok := headerLookup(h, "authorization"); ok {
		t.Fatal("absent header reported present")
	}
}

func TestInferContentType(t *testing.T) {
	if inferContentType(`{"a":1}`) != "application/json" {
		t.Fatal("object body should infer JSON")
	}
	if inferContentType(`  [1,2]`) != "application/json" {
		t.Fatal("array body should infer JSON")
	}
	if inferContentType("plain text") != "" {
		t.Fatal("non-JSON body should infer nothing")
	}
}

// TestHTTPRequestLive makes a real fingerprinted request. It is gated on
// ANTARES_LIVE_HTTP because it needs the network.
func TestHTTPRequestLive(t *testing.T) {
	if os.Getenv("ANTARES_LIVE_HTTP") == "" {
		t.Skip("set ANTARES_LIVE_HTTP=1 to run the live request")
	}
	c := httpClientFor(defaultHTTPPreset, "")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	resp, err := c.Get(ctx, "https://www.cloudflare.com/cdn-cgi/trace")
	if err != nil {
		t.Fatalf("live request failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := strings.ToLower(string(resp.Body))
	// Cloudflare's trace echoes the connection it saw: uag= reports the Chrome
	// user-agent and http= the negotiated protocol, proving the fingerprinted
	// stack completed a real handshake through Cloudflare's edge.
	t.Logf("protocol=%s body=%.300s", resp.Protocol, body)
	if !strings.Contains(body, "fl=") {
		t.Fatalf("unexpected body, endpoint not reached: %.200s", body)
	}
}
