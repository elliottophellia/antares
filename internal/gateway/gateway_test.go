package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestSyncBeforeStart(t *testing.T) {
	m := NewManager(&config.Config{}, nil, nil)
	// Sync depends on the process context that Start records; asking for a
	// reconnect before then must say so rather than panic on a nil context.
	if err := m.Sync("telegram"); err == nil {
		t.Fatal("expected an error when the gateway has not started")
	}
}

func TestSyncDisabledGatewayStopsAdapters(t *testing.T) {
	cfg := &config.Config{}
	m := NewManager(cfg, nil, nil)
	m.Start(context.Background())

	if err := m.Sync("telegram"); err != nil {
		t.Fatalf("sync with the gateway off: %v", err)
	}
	if len(m.Status()) != 0 {
		t.Fatalf("expected no adapters, got %v", m.Status())
	}
}

func TestSyncUnknownPlatform(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Enabled = true
	m := NewManager(cfg, nil, nil)
	m.Start(context.Background())

	err := m.Sync("carrier-pigeon")
	if err == nil || !strings.Contains(err.Error(), "carrier-pigeon") {
		t.Fatalf("expected the platform name in the error, got %v", err)
	}
}

func TestSetConfigIsVisibleToSync(t *testing.T) {
	m := NewManager(&config.Config{}, nil, nil)
	m.Start(context.Background())

	// A reload replaces the whole config pointer; Sync has to reconcile against
	// the new one, not the one the manager was built with.
	next := &config.Config{}
	next.Gateway.Enabled = true
	m.SetConfig(next)

	if m.config() != next {
		t.Fatal("SetConfig did not take effect")
	}
	if err := m.Sync("telegram"); err != nil {
		t.Fatalf("sync after reload: %v", err)
	}
	// No token, so nothing should have started.
	if len(m.Status()) != 0 {
		t.Fatalf("expected no adapters without a token, got %v", m.Status())
	}
}
