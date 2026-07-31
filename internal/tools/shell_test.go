package tools

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func TestDefaultShellDoesNotInheritNonPOSIXShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell selection does not apply on Windows")
	}

	old := os.Getenv("SHELL")
	t.Cleanup(func() { _ = os.Setenv("SHELL", old) })
	if err := os.Setenv("SHELL", "/bin/fish"); err != nil {
		t.Fatal(err)
	}

	shell, _ := defaultShell("")
	if shell == "/bin/fish" {
		t.Fatalf("defaultShell inherited %q, but the sentinel protocol requires a POSIX shell", shell)
	}
	if shell != "/bin/bash" && shell != "/bin/sh" {
		t.Fatalf("defaultShell = %q, want /bin/bash or /bin/sh", shell)
	}
}

func TestDefaultShellHonorsExplicitConfiguration(t *testing.T) {
	shell, args := defaultShell("/custom/shell")
	if shell != "/custom/shell" {
		t.Fatalf("defaultShell(configured) = %q", shell)
	}
	if len(args) != 1 || args[0] != "-i" {
		t.Fatalf("configured shell args = %#v, want [-i]", args)
	}
}

func TestDefaultShellCommandEmitsCompletionSentinel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent shell protocol does not apply on Windows")
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	sess, err := m.session("test-session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	out, code, err := sess.run(context.Background(), "printf ANTARES_SHELL_OK", 2*time.Second, nil)
	if err != nil {
		t.Fatalf("command did not complete via sentinel: %v", err)
	}
	if code != 0 || out != "ANTARES_SHELL_OK" {
		t.Fatalf("command result = (%q, %d), want (%q, 0)", out, code, "ANTARES_SHELL_OK")
	}
}
