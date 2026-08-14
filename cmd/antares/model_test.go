package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

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
