package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A line number is only worth reporting if every tool means the same thing by
// it. The agent reads a file, is handed a line number, and then greps or edits
// against it; when the tools count differently that number points at the wrong
// line, or at a line that does not exist.
//
// Each file here holds three lines, with a token on the third. read_file's
// total, grep's match line and edit_file's occurrence lines must all say 3,
// whichever sequence terminates the lines and whether or not the last line is
// terminated at all.
func TestLineNumbersAgreeAcrossTools(t *testing.T) {
	for _, tc := range []struct{ name, eol, tail string }{
		{"lf", "\n", "\n"},
		{"crlf", "\r\n", "\r\n"},
		{"cr", "\r", "\r"},
		{"no trailing terminator", "\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The token twice on one line, so a single ambiguous edit_file call
			// reports occurrence lines without a second line to confuse them.
			body := "alpha" + tc.eol + "beta" + tc.eol + "NEEDLE NEEDLE" + tc.tail
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "sample.txt"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}

			if got := readFileTotalLines(t, workspace, "sample.txt"); got != 3 {
				t.Errorf("read_file reports %d lines, want 3", got)
			}
			if got := grepMatchLines(t, workspace, "NEEDLE"); len(got) != 1 || got[0] != 3 {
				t.Errorf("grep reports match line(s) %v, want [3]", got)
			}
			if got := editOccurrenceLines(t, workspace, "sample.txt", "NEEDLE"); len(got) == 0 {
				t.Error("edit_file reported no occurrence lines for an ambiguous anchor")
			} else {
				for _, line := range got {
					if line != 3 {
						t.Errorf("edit_file reports occurrence line(s) %v, want every one to be 3", got)
						break
					}
				}
			}
		})
	}
}

// readFileTotalLines returns the total read_file reports for a whole-file read.
// The total comes from Meta so this stays a test about counting rather than
// about how the content happens to be rendered.
func readFileTotalLines(t *testing.T, workspace, name string) int {
	t.Helper()
	args, err := json.Marshal(map[string]any{"path": name})
	if err != nil {
		t.Fatal(err)
	}
	res := (readFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if res.IsError {
		t.Fatalf("read_file failed: %s", res.Content)
	}
	for _, key := range []string{"total_lines", "lines"} {
		if v, ok := res.Meta[key]; ok {
			switch n := v.(type) {
			case int:
				return n
			case float64:
				return int(n)
			}
			t.Fatalf("read_file Meta[%q] = %v (%T), want a number", key, v, v)
		}
	}
	t.Fatalf("read_file reported no line total in Meta: %v", res.Meta)
	return 0
}

var grepLinePrefix = regexp.MustCompile(`(?m)^\s*(\d+):\t`)

// grepMatchLines returns the line numbers grep printed for its matches.
func grepMatchLines(t *testing.T, workspace, pattern string) []int {
	t.Helper()
	args, err := json.Marshal(map[string]any{"pattern": pattern, "path": "."})
	if err != nil {
		t.Fatal(err)
	}
	res := (grepTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	var lines []int
	for _, m := range grepLinePrefix.FindAllStringSubmatch(res.Content, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("grep printed a line number that will not parse: %q", m[1])
		}
		lines = append(lines, n)
	}
	if len(lines) == 0 {
		t.Fatalf("grep found nothing to report a line number for: %q", res.Content)
	}
	return lines
}

var editOccurrenceList = regexp.MustCompile(`Current match line\(s\): ([\d, ]+)\.`)

// editOccurrenceLines returns the line numbers edit_file names when it refuses
// an anchor for appearing more than once, which is the only path that reports
// them.
func editOccurrenceLines(t *testing.T, workspace, name, oldString string) []int {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"path": name, "old_string": oldString, "new_string": oldString + "_EDITED",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !res.IsError {
		t.Fatalf("edit_file accepted an anchor that appears twice: %s", res.Content)
	}
	m := editOccurrenceList.FindStringSubmatch(res.Content)
	if m == nil {
		t.Fatalf("edit_file named no occurrence lines: %s", res.Content)
	}
	var lines []int
	for _, field := range strings.Split(m[1], ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil {
			t.Fatalf("edit_file printed a line number that will not parse: %q", field)
		}
		lines = append(lines, n)
	}
	return lines
}

// lineSpans is the one splitter the file tools count with, so its own rules are
// worth stating directly: a terminated last line adds no empty line after it,
// and 1-based numbering runs to exactly the number of lines the file has.
func TestLineSpansCountsTerminatedAndUnterminatedFilesAlike(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    int
	}{
		{"empty file", "", 0},
		{"lf terminated", "a\nb\n", 2},
		{"lf unterminated", "a\nb", 2},
		{"crlf terminated", "a\r\nb\r\n", 2},
		{"cr terminated", "a\rb\r", 2},
		{"blank line before the end", "a\n\n", 2},
		{"lone cr is data in an lf file", "a\rb\nc\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(lineSpans(tc.content)); got != tc.want {
				t.Errorf("lineSpans(%q) = %d lines, want %d", tc.content, got, tc.want)
			}
		})
	}
}

// grep's context lines carry line numbers too, and they are counted backwards
// from the match rather than tracked as they are read. They come from the same
// splitter as the match itself, so they have to stay in step with it.
func TestGrepNumbersContextLinesFromTheSameSplitter(t *testing.T) {
	workspace := t.TempDir()
	body := "alpha\r\nbeta\r\nNEEDLE\r\ndelta\r\n"
	if err := os.WriteFile(filepath.Join(workspace, "sample.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"pattern": "NEEDLE", "path": ".", "context": 1})
	res := (grepTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	for _, want := range []string{"     2-\tbeta", "     3:\tNEEDLE", "     4-\tdelta"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("grep did not report %q: %q", want, res.Content)
		}
	}
}

// An offset past the last line is a mistake worth naming. Returning nothing at
// all reads as "the file is empty from here", which is a different fact. Line 3
// of a two-line file was reachable only because the count included a phantom
// trailing line.
func TestReadFileRejectsAnOffsetPastTheLastLine(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "two.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "two.txt", "offset": 3})
	res := (readFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !res.IsError {
		t.Fatalf("offset 3 on a two-line file returned %q, want an error", res.Content)
	}
	if !strings.Contains(res.Content, "(2 lines)") {
		t.Errorf("error does not report the real line count: %s", res.Content)
	}
}
