package agent

import (
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func TestCursorToolTimeoutsAllowLongCloudRuns(t *testing.T) {
	a := agentWithConfig(config.Default())
	for _, name := range []string{"cursor_agent", "cursor_agent_status"} {
		if got := a.toolTimeout(name); got < 16*time.Minute {
			t.Fatalf("%s timeout = %s, want at least 16m", name, got)
		}
	}
}

func TestCursorToolTimeoutDefaultsRemainOperatorOverrideable(t *testing.T) {
	cfg := config.Default()
	for _, name := range []string{"cursor_agent", "cursor_agent_status"} {
		if got := cfg.Tools.Timeouts[name]; got != 960 {
			t.Fatalf("%s default timeout = %d, want 960", name, got)
		}
	}

	cfg.Tools.Timeouts["cursor_agent"] = 123
	a := agentWithConfig(cfg)
	if got := a.toolTimeout("cursor_agent"); got != 123*time.Second {
		t.Fatalf("cursor_agent configured timeout = %s, want 123s", got)
	}
}
