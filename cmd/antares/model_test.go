package main

import (
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

// `antares model <id> cursor` used to persist an agent integration as the
// active chat provider, which every other selector already refuses.
func TestModelCommandRejectsAgentProvider(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	cfg := config.Default()
	beforeProvider, beforeModel := cfg.Model.Provider, cfg.Model.Default
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	err := cmdModel([]string{"claude-sonnet-5", "cursor"})
	if err == nil || !strings.Contains(err.Error(), "cursor_agent") {
		t.Fatalf("model set to cursor error = %v, want an agent-integration refusal", err)
	}

	after, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if after.Model.Provider != beforeProvider || after.Model.Default != beforeModel {
		t.Fatalf("active model changed to %s (%s), want %s (%s)",
			after.Model.Default, after.Model.Provider, beforeModel, beforeProvider)
	}
}

func TestModelCommandStillSwitchesLLMProvider(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	output := captureProviderStdout(t, func() {
		if err := cmdModel([]string{"gpt-5", "openai"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "active model: gpt-5 (openai)") {
		t.Fatalf("model set output = %q", output)
	}

	after, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if after.Model.Provider != "openai" || after.Model.Default != "gpt-5" {
		t.Fatalf("active model = %s (%s), want gpt-5 (openai)", after.Model.Default, after.Model.Provider)
	}
}
