package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFileResult drives the real tool and fails the test if the read was
// refused, so each case below asserts about content rather than about plumbing.
func readFileResult(t *testing.T, workspace string, args map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res := (readFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: raw})
	if res.IsError {
		t.Fatalf("read_file failed: %s", res.Content)
	}
	return res
}

// readFileBody returns what read_file put below its header. The header is one
// line, so the first blank line in the output is the one that ends it.
func readFileBody(t *testing.T, out string) string {
	t.Helper()
	_, body, ok := strings.Cut(out, "\n\n")
	if !ok {
		t.Fatalf("read_file output has no header line followed by a blank line: %q", out)
	}
	return body
}

// What read_file returns is what the model must hand back as an edit_file
// anchor, so anything the tool adds to a line is something the model has to
// remove again — and a separator stamped in front of the content collides with
// content that legitimately starts with it, most visibly a markdown table row.
// Returning the file's own bytes is what makes a copied region an exact anchor.
func TestReadFileReturnsVerbatim(t *testing.T) {
	workspace := t.TempDir()
	// Tab indentation, one CRLF line among LF lines, a lone CR that is data
	// rather than a terminator, and rows beginning with a pipe.
	original := "def main():\n" +
		"\tif enabled:\r\n" +
		"\t\trun()  # ends here\rand continues\n" +
		"\n" +
		"| Date | Event |\n" +
		"|------|-------|\n" +
		"| 2026-01-01 | ship |\n"
	if err := os.WriteFile(filepath.Join(workspace, "sample.py"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res := readFileResult(t, workspace, map[string]any{"path": "sample.py"})
	header := "sample.py — lines 1-7 of 7"
	if !strings.HasPrefix(res.Content, header+"\n\n") {
		t.Fatalf("read_file does not open with %q and a blank line:\n%q", header, res.Content)
	}
	body := strings.TrimPrefix(res.Content, header+"\n\n")
	if body != original {
		t.Fatalf("read_file returned a rendering of the file, not the file:\ngot  %q\nwant %q", body, original)
	}

	// The workflow the format exists for: copy a region out of what read_file
	// returned and hand it straight back as an anchor. Cut it out of the body
	// rather than writing it out again, so this still means something if the
	// body ever stops being the file.
	lines := strings.SplitAfter(body, "\n")
	if len(lines) < 4 {
		t.Fatalf("body does not hold the file's lines: %q", body)
	}
	copied := strings.Join(lines[1:3], "")
	if !strings.Contains(original, copied) {
		t.Errorf("a region copied out of read_file output is not in the file: %q", copied)
	}
}

// Rendering each line as "%s\n" gave an unterminated last line a newline the
// file does not have, so an anchor copied from the end of a file matched
// nothing.
func TestReadFileDoesNotTerminateAnUnterminatedLastLine(t *testing.T) {
	workspace := t.TempDir()
	original := "alpha\nbeta"
	if err := os.WriteFile(filepath.Join(workspace, "tail.txt"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res := readFileResult(t, workspace, map[string]any{"path": "tail.txt"})
	if body := readFileBody(t, res.Content); body != original {
		t.Fatalf("body = %q, want the file's bytes %q", body, original)
	}
}

// The line range moves from the content into the header and Meta, so a clipped
// read still says which lines it covers and how many there are — the numbers
// grep and edit_file report against.
func TestReadFileHeaderAndMetaDescribeTheRange(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := readFileResult(t, workspace, map[string]any{"path": "notes.txt", "offset": 2, "limit": 2})

	want := "notes.txt — lines 2-3 of 5\n\ntwo\nthree\n"
	if !strings.HasPrefix(res.Content, want) {
		t.Fatalf("read_file output = %q, want it to begin %q", res.Content, want)
	}
	if !strings.Contains(res.Content, "… 2 more lines (use offset=4 to continue)") {
		t.Errorf("clipped read does not say how to continue: %q", res.Content)
	}
	for key, want := range map[string]any{
		"path": "notes.txt", "first_line": 2, "last_line": 3, "total_lines": 5,
	} {
		if got := res.Meta[key]; got != want {
			t.Errorf("Meta[%q] = %v, want %v", key, got, want)
		}
	}
	if _, ok := res.Meta["lines"]; ok {
		t.Errorf("Meta still carries the ambiguous \"lines\" key: %v", res.Meta)
	}
}

// A file with nothing in it has no line 1, so the header names no line rather
// than inventing one, and still reports the total the rest of the tools use.
func TestReadFileHeaderOnAnEmptyFile(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res := readFileResult(t, workspace, map[string]any{"path": "empty.txt"})
	if want := "empty.txt — lines 0-0 of 0\n\n"; res.Content != want {
		t.Fatalf("read_file on an empty file = %q, want %q", res.Content, want)
	}
	if got := res.Meta["total_lines"]; got != 0 {
		t.Errorf("Meta[\"total_lines\"] = %v, want 0", got)
	}
}
