package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// bigLog builds a log past the 400 KB read cap out of fixed-width lines, so the
// line the cap lands on is arithmetic rather than a guess: 40 bytes a line puts
// exactly 10240 lines inside the cap, with no partial line at the seam.
func bigLog(t *testing.T, lines int, needleAt int, needle string) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		text := fmt.Sprintf("log line %d", i)
		if i == needleAt {
			text = needle
		}
		if len(text) > 39 {
			t.Fatalf("line %d does not fit the fixed width: %q", i, text)
		}
		fmt.Fprintf(&b, "%-39s\n", text)
	}
	return b.String()
}

var readHeaderRange = regexp.MustCompile(`^(.*) — lines (\d+)-(\d+) of (≥?)(\d+)\n`)

// headerRange returns the three numbers read_file's header states and whether
// the total is stated as a lower bound rather than as a count.
func headerRange(t *testing.T, out string) (first, last, total int, isLowerBound bool) {
	t.Helper()
	m := readHeaderRange.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("read_file output does not open with a line-range header: %q", firstBytes(out, 120))
	}
	var n [3]int
	for i, field := range []string{m[2], m[3], m[5]} {
		v, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("header states a number that will not parse: %q", m[0])
		}
		n[i] = v
	}
	return n[0], n[1], n[2], m[4] == "≥"
}

// The cap stops the read after 400 KB, so the lines behind it were never
// counted. Reporting the lines that were read as the file's total is a claim
// about bytes the tool declined to look at, and it is the claim grep
// contradicts: grep counts the whole file, so it hands back line numbers this
// tool then calls impossible. A number the caller cannot act on is worse than
// no number, because it looks like one.
func TestReadFileStatesNoTotalItHasNotCounted(t *testing.T) {
	const (
		lines  = 15360 // 600 KB at 40 bytes a line
		needle = "NEEDLE_HERE"
	)
	workspace := t.TempDir()
	body := bigLog(t, lines, 15001, needle)
	if err := os.WriteFile(filepath.Join(workspace, "big.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := len(lineSpans(body)); got != lines {
		t.Fatalf("fixture is %d lines, want %d", got, lines)
	}

	t.Run("the header states no total the read did not count", func(t *testing.T) {
		res := readFileResult(t, workspace, map[string]any{"path": "big.log", "limit": lines})
		_, _, total, isLowerBound := headerRange(t, res.Content)
		switch {
		case isLowerBound && total > lines:
			t.Errorf("header claims at least %d lines, and the file has %d", total, lines)
		case !isLowerBound && total != lines:
			t.Errorf("header states %d lines as a fact; the file has %d, and the read stopped at the cap without counting them", total, lines)
		}
	})

	// The metadata is offered so a caller does not have to parse the header
	// (docs/tools.md), so every number the header states has to be in it. The
	// default limit is where that is easiest to get wrong: it stops the read
	// long before the byte cap does, so the last line returned and the lines
	// the cap held are different numbers, and only one of them was in Meta.
	t.Run("Meta carries every number the default read's header states", func(t *testing.T) {
		res := readFileResult(t, workspace, map[string]any{"path": "big.log"})
		first, last, floor, isLowerBound := headerRange(t, res.Content)
		if !isLowerBound || last >= floor {
			t.Fatalf("this case needs a read the line limit clips before the byte cap does; the header says lines %d-%d of %d", first, last, floor)
		}
		for key, want := range map[string]int{
			"first_line": first, "last_line": last, "total_lines_at_least": floor,
		} {
			got, ok := res.Meta[key]
			if !ok {
				t.Errorf("the header states %d and Meta has no %q, so a caller wanting that number has to parse the header after all: %v", want, key, res.Meta)
				continue
			}
			if got != want {
				t.Errorf("Meta[%q] = %v, and the header says %d", key, got, want)
			}
		}
	})

	t.Run("Meta states no total the read did not count", func(t *testing.T) {
		res := readFileResult(t, workspace, map[string]any{"path": "big.log", "limit": lines})
		if got, ok := res.Meta["total_lines"]; ok && got != lines {
			t.Errorf("Meta[\"total_lines\"] = %v, and the file has %d lines; a total the read never counted does not belong in it", got, lines)
		}
	})

	// The continuation note carries the same claim in another form: a caller
	// paging through the file is told how much is left.
	t.Run("the continuation note states no remainder the read did not count", func(t *testing.T) {
		res := readFileResult(t, workspace, map[string]any{"path": "big.log", "limit": 2000})
		m := regexp.MustCompile(`… (?:at least )?(\d+) more lines`).FindStringSubmatch(res.Content)
		if m == nil {
			t.Fatalf("a read of 2000 lines out of %d appended no continuation note: %q", lines, firstBytes(res.Content, 160))
		}
		more, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		if want := lines - 2000; more != want && !strings.Contains(m[0], "at least") {
			t.Errorf("note says %d more lines as a fact; %d follow", more, want)
		}
		if more > lines-2000 {
			t.Errorf("note claims at least %d more lines; only %d follow", more, lines-2000)
		}
	})

	// The handoff the numbers exist for. grep reports the needle's line; the
	// natural next call is read_file at that offset, and answering it with
	// "past end of file" denies a line the file has.
	t.Run("grep's line number is not refused as past the end of the file", func(t *testing.T) {
		at := grepMatchLines(t, workspace, needle)
		if len(at) != 1 || at[0] != 15001 {
			t.Fatalf("grep reports the needle at %v, want [15001]", at)
		}
		res := readFileArgs(t, workspace, map[string]any{"path": "big.log", "offset": at[0]})
		if !res.IsError {
			return // the read served the line, which is more than the claim needs
		}
		if strings.Contains(res.Content, "past end of file") {
			t.Errorf("read_file denies a line grep just read: %s", res.Content)
		}
		if !strings.Contains(res.Content, "400 KB") {
			t.Errorf("refusal does not name the cap that caused it: %s", res.Content)
		}
	})
}

// readFileArgs drives read_file and returns whatever it produced, error or not.
// readFileResult next door fails the test on an error result, which is right
// for a test about content and wrong for one about a refusal.
func readFileArgs(t *testing.T, workspace string, args map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return (readFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: raw})
}
