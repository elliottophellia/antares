package tui

import (
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestCursorProviderConnectAndSelectPreserveActiveModel(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	cfg := config.Default()
	beforeProvider, beforeModel := cfg.Model.Provider, cfg.Model.Default
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m := &Model{cfg: cfg}

	m.activateProvider("cursor", "synthetic-key")
	if m.cfg.Model.Provider != beforeProvider || m.cfg.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", m.cfg.Model.Provider, m.cfg.Model.Default)
	}
	connected, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if p := connected.Providers["cursor"]; !p.Enabled || p.APIKey != "synthetic-key" || p.Kind != "cursor-agent" {
		t.Fatalf("cursor provider = %+v", p)
	}

	m.selectProvider("cursor")
	if m.cfg.Model.Provider != beforeProvider || m.cfg.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", m.cfg.Model.Provider, m.cfg.Model.Default)
	}
	if len(m.blocks) == 0 || !strings.Contains(m.blocks[len(m.blocks)-1].text, "cursor_agent") {
		t.Fatalf("system message = %+v", m.blocks)
	}
}
