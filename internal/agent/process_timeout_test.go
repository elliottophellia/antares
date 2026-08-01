package agent

import (
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func TestProcessToolTimeoutLeavesWaitMargin(t *testing.T) {
	a := &Agent{cfg: &config.Config{}}
	if got := a.toolTimeout("process"); got < 45*time.Second {
		t.Fatalf("process tool timeout = %s, want at least 45s for a 30s process wait", got)
	}
}
