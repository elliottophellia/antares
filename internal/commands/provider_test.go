package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestProviderCommandRejectsAgentIntegration(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	cfg := config.Default()
	beforeProvider, beforeModel := cfg.Model.Provider, cfg.Model.Default
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	reloaded := false
	result, err := Run(context.Background(), Deps{
		Config: func() *config.Config { return cfg },
		Reload: func() error {
			reloaded = true
			return nil
		},
	}, Input{Name: "provider", Args: "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "cursor_agent") {
		t.Fatalf("result = %+v", result)
	}
	if result.Action.Kind != "" || reloaded {
		t.Fatalf("agent provider changed configuration: result=%+v reloaded=%v", result, reloaded)
	}

	after, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if after.Model.Provider != beforeProvider || after.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", after.Model.Provider, after.Model.Default)
	}
}
