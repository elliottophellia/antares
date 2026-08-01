package tools

import (
	"context"
	"os"
	"runtime"
	"strings"
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

func TestPersistentShellErrexitDoesNotLeakBetweenCalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell options do not apply on Windows")
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	sess, err := m.session("errexit-session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, code, err := sess.run(context.Background(), "set -e; true", 2*time.Second, nil); err != nil || code != 0 {
		t.Fatalf("enabling errexit = code %d, err %v", code, err)
	}

	start := time.Now()
	_, code, err := sess.run(context.Background(), "printf x | grep definitely-no-match", 2*time.Second, nil)
	if err != nil {
		t.Fatalf("no-match pipeline after prior set -e did not reach sentinel: %v", err)
	}
	if code != 1 {
		t.Fatalf("no-match pipeline exit code = %d, want 1", code)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("failure after prior set -e took %s, want prompt completion", elapsed)
	}
}

func TestPersistentShellExitReturnsPromptlyAndRecovers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell exit behavior does not apply on Windows")
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	workspace := t.TempDir()
	sess, err := m.session("exit-session", workspace)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, _, err = sess.run(context.Background(), "set -e; false", 5*time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "shell exited") {
		t.Fatalf("shell exit error = %v, want shell exited", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shell exit detected after %s, want under 1s", elapsed)
	}

	replacement, err := m.session("exit-session", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if replacement == sess {
		t.Fatal("dead persistent shell was reused")
	}
	out, code, err := replacement.run(context.Background(), "printf RECOVERED", 2*time.Second, nil)
	if err != nil || code != 0 || out != "RECOVERED" {
		t.Fatalf("replacement shell result = (%q, %d, %v)", out, code, err)
	}
}
