package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestProviderAddAndUseCursorPreserveActiveModel(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	cfg := config.Default()
	beforeProvider, beforeModel := cfg.Model.Provider, cfg.Model.Default
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	output := captureProviderStdout(t, func() {
		if err := cmdProvider([]string{"add", "cursor", "synthetic-key"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "connected cursor agent integration") {
		t.Fatalf("add output = %q", output)
	}
	connected, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if connected.Model.Provider != beforeProvider || connected.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", connected.Model.Provider, connected.Model.Default)
	}
	if p := connected.Providers["cursor"]; !p.Enabled || p.APIKey != "synthetic-key" || p.Kind != "cursor-agent" {
		t.Fatalf("cursor provider = %+v", p)
	}

	err = cmdProvider([]string{"use", "cursor"})
	if err == nil || !strings.Contains(err.Error(), "cursor_agent") {
		t.Fatalf("use cursor error = %v", err)
	}
	afterUse, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if afterUse.Model.Provider != beforeProvider || afterUse.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", afterUse.Model.Provider, afterUse.Model.Default)
	}
}

func captureProviderStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })
	f()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
