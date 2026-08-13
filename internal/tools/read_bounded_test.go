package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The 400 KB cap decides what reaches the model. What reaches the process is a
// separate question, and until the read itself is bounded the cap answers it
// only by accident: a file is loaded whole and then trimmed, so an ordinary
// workspace file that is simply large takes the process's memory with it before
// any of this code runs. grep's gate was moved onto the read for exactly this
// reason (search.go), and read_file's has to be too.
func TestReadFileBoundsTheReadAndNotOnlyTheResult(t *testing.T) {
	// A large file needs no device node and no project session to reach: a
	// core dump, a vendored bundle or a captured log inside the workspace is
	// enough, and holding it whole is the cost the cap was supposed to remove.
	t.Run("an oversized file is never held whole", func(t *testing.T) {
		const size = 64 << 20
		workspace := t.TempDir()
		writeSparseFile(t, filepath.Join(workspace, "core.dump"), "header line\n", size)
		args, err := json.Marshal(map[string]any{"path": "core.dump"})
		if err != nil {
			t.Fatal(err)
		}

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		res := (readFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
		runtime.ReadMemStats(&after)
		if res.IsError {
			t.Fatalf("read_file failed on a large text file: %s", res.Content)
		}

		// Generous next to the 64 MB file and far above what a bounded read of
		// 400 KB costs, so this fails on the defect rather than on allocator
		// noise.
		const budget = 8 << 20
		if grew := after.TotalAlloc - before.TotalAlloc; grew > budget {
			t.Errorf("read_file allocated %d bytes to return 400 KB of a %d-byte file; the cap has to bound the read, not trim what was already read", grew, size)
		}
	})

	// Nor is the size a file states a bound. A character device, most of /proc
	// and /sys, and a file being appended to during the read all yield more
	// than stat promised; /dev/zero reports zero bytes and never ends. A
	// project session leaves reads unconfined, so a path like this one is
	// reachable through resolveRead as written.
	t.Run("a file that understates its size is still bounded", func(t *testing.T) {
		if _, err := os.Stat("/dev/zero"); err != nil {
			t.Skip("no /dev/zero to read from on this platform")
		}
		workspace := t.TempDir()
		args, err := json.Marshal(map[string]any{"path": "/dev/zero"})
		if err != nil {
			t.Fatal(err)
		}

		// Off the test goroutine, so an unbounded read fails the test instead
		// of hanging the package until the go test deadline.
		done := make(chan Result, 1)
		go func() {
			done <- (readFileTool{}).Execute(context.Background(), Input{
				Workspace: workspace, WriteRoots: []string{workspace}, Args: args,
			})
		}()
		select {
		case res := <-done:
			if res.IsError {
				t.Fatalf("read_file failed: %s", firstBytes(res.Content, 200))
			}
			if !strings.Contains(res.Content, "truncated at 400 KB") {
				t.Errorf("an endless file came back without the truncation notice: %q", firstBytes(res.Content, 200))
			}
		case <-time.After(10 * time.Second):
			t.Fatal("read_file did not return: the read is not bounded by the size cap")
		}
	})
}

// firstBytes keeps a failure message readable when the subject is a 400 KB read
// of NUL bytes.
func firstBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
