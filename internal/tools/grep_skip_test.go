package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The size gate keeps grep from opening large files at all, so a run that hit
// it cannot tell whether the pattern is there. Reporting only "No matches"
// reads as "not present", and it does so on exactly the large log and data
// files people reach for grep to search.
func TestGrepReportsSkippedFiles(t *testing.T) {
	t.Run("a skipped file is reported", func(t *testing.T) {
		workspace := t.TempDir()
		writeSparseFile(t, filepath.Join(workspace, "huge.log"), "NEEDLE_TOKEN\n", 9*1024*1024)

		result := grepWorkspace(t, workspace, "NEEDLE_TOKEN")
		if result.IsError {
			t.Fatalf("grep errored: %s", result.Content)
		}
		for _, want := range []string{"1 file(s)", "8 MB", "not searched"} {
			if !strings.Contains(result.Content, want) {
				t.Errorf("skip report is missing %q, got: %q", want, result.Content)
			}
		}
	})

	t.Run("a file named directly is gated by the same size", func(t *testing.T) {
		workspace := t.TempDir()
		writeSparseFile(t, filepath.Join(workspace, "huge.log"), "NEEDLE_TOKEN\n", 9*1024*1024)

		args, err := json.Marshal(map[string]any{"pattern": "NEEDLE_TOKEN", "path": "huge.log"})
		if err != nil {
			t.Fatal(err)
		}
		result := (grepTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
		if result.IsError {
			t.Fatalf("grep errored: %s", result.Content)
		}
		if !strings.Contains(result.Content, "not searched") {
			t.Errorf("a directly named file above the cap was silently unsearched: %q", result.Content)
		}
	})

	// The gate has to bound the read, not just consult the size the file claims.
	// A character device, most of /proc and /sys, and a file being appended to
	// during the read all yield more than stat promised; /dev/zero reports zero
	// bytes and never ends. A symlink to it is an ordinary entry to
	// filepath.WalkDir, so a plain recursive grep reaches it without a project
	// session, and an unbounded read there takes the process's memory with it.
	t.Run("a file that understates its size is still bounded", func(t *testing.T) {
		if _, err := os.Stat("/dev/zero"); err != nil {
			t.Skip("no /dev/zero to read from on this platform")
		}
		workspace := t.TempDir()
		if err := os.Symlink("/dev/zero", filepath.Join(workspace, "zero.log")); err != nil {
			t.Skipf("cannot create a symlink in the workspace: %v", err)
		}
		args, err := json.Marshal(map[string]any{"pattern": "NEEDLE_TOKEN", "path": "."})
		if err != nil {
			t.Fatal(err)
		}

		// Run it off the test goroutine so an unbounded read fails the test
		// instead of hanging the package until the go test deadline.
		done := make(chan Result, 1)
		go func() {
			done <- (grepTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
		}()
		select {
		case result := <-done:
			if result.IsError {
				t.Fatalf("grep errored: %s", result.Content)
			}
			if !strings.Contains(result.Content, "not searched") {
				t.Errorf("an endless file was not reported as skipped: %q", result.Content)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("grep did not return: the read is not bounded by the size gate")
		}
	})

	t.Run("a run that skipped nothing says nothing", func(t *testing.T) {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "small.log"), []byte("NEEDLE_TOKEN here\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := grepWorkspace(t, workspace, "NEEDLE_TOKEN")
		if result.IsError {
			t.Fatalf("grep errored: %s", result.Content)
		}
		if !strings.Contains(result.Content, "NEEDLE_TOKEN here") {
			t.Fatalf("small file should have matched, got: %q", result.Content)
		}
		if strings.Contains(result.Content, "warning") {
			t.Fatalf("nothing was skipped, so nothing should be warned about, got: %q", result.Content)
		}
	})
}

func grepWorkspace(t *testing.T, workspace, pattern string) Result {
	t.Helper()
	args, err := json.Marshal(map[string]any{"pattern": pattern, "path": "."})
	if err != nil {
		t.Fatal(err)
	}
	return (grepTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
}

// writeSparseFile writes head and then extends the file to size with a hole, so
// a file that reports megabytes costs the test only the bytes in head.
func writeSparseFile(t *testing.T, path, head string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(head); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}
