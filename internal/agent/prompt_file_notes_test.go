package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// filePrompt assembles the system prompt with only the two file tools active,
// so the assertions below are about the read → edit guidance and nothing else.
func filePrompt(t *testing.T) string {
	t.Helper()
	cfg := config.Default()
	cfg.Memory.Enabled = false
	a := agentWithConfig(cfg)
	sess := &store.Session{ID: "s", Workspace: "/workspace", Meta: store.Meta{}}
	var active []tools.Tool
	for _, name := range []string{"read_file", "edit_file"} {
		tool, ok := tools.Default().Get(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		active = append(active, tool)
	}
	return a.buildSystemPrompt(context.Background(), Request{}, sess, active)
}

// The prompt is the only description of the tools the model ever sees, so an
// instruction that no longer matches them is not stale documentation — it is a
// standing order to corrupt data. The instruction that was there told the model
// to keep only what follows a "|", and a list of the phrases it used cannot
// guard against it: the same order rewritten around a colon reads as new advice
// and passes every one of them. grep prints "%6d:\t" from the package next
// door, so a line of spaces, digits and a colon genuinely is a line number
// somewhere in this repository — just not in read_file's output.
//
// What does not depend on the wording is the tool. This drives it on content
// shaped like the numbering such an instruction would describe, and checks the
// two things the bullet tells the model to do with what comes back: copy a
// region straight into old_string, which has to work, and strip nothing from
// it, because stripping is what breaks the file.
func TestPromptClaimThatACopiedRegionIsAnAnchorHoldsAgainstTheRealTools(t *testing.T) {
	// Line 1 is grep's own output shape, line 2 the pipe-separated row the
	// removed format collided with, and line 4 is tab-indented.
	const original = "   288:\tfmt.Fprintf(&b, \"%6d:\\t%s\\n\", lineNo, line)\n" +
		"12|alice|admin\n" +
		"| Date | Event |\n" +
		"\tif enabled:\n"
	// One row promoted, and nothing else in the file touched.
	const intended = "   288:\tfmt.Fprintf(&b, \"%6d:\\t%s\\n\", lineNo, line)\n" +
		"12|alice|owner\n" +
		"| Date | Event |\n" +
		"\tif enabled:\n"

	read, ok := tools.Default().Get("read_file")
	if !ok {
		t.Fatal("read_file is not registered")
	}
	edit, ok := tools.Default().Get("edit_file")
	if !ok {
		t.Fatal("edit_file is not registered")
	}
	// A workspace holding the file, and the region the model would copy: cut
	// out of what read_file returned rather than written out again, so it is
	// the tool's output that is under test and not the fixture.
	setUp := func(t *testing.T) (workspace, copied string) {
		t.Helper()
		workspace = t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "rows.txt"), []byte(original), 0o644); err != nil {
			t.Fatal(err)
		}
		res := read.Execute(context.Background(), tools.Input{
			Workspace: workspace,
			Args:      []byte(`{"path":"rows.txt"}`),
		})
		if res.IsError {
			t.Fatalf("read_file: %s", res.Content)
		}
		_, body, ok := strings.Cut(res.Content, "\n\n")
		if !ok {
			t.Fatalf("read_file output has no header line followed by a blank line: %q", res.Content)
		}
		if body != original {
			t.Fatalf("prompt promises the file's exact bytes, read_file returned %q", body)
		}
		lines := strings.Split(body, "\n")
		if len(lines) < 2 {
			t.Fatalf("read_file returned no second line to copy: %q", body)
		}
		return workspace, lines[1]
	}

	editRow := func(t *testing.T, workspace, oldString, newString string) (said string, after string) {
		t.Helper()
		args, err := json.Marshal(map[string]any{
			"path": "rows.txt", "old_string": oldString, "new_string": newString,
		})
		if err != nil {
			t.Fatal(err)
		}
		res := edit.Execute(context.Background(), tools.Input{Workspace: workspace, Args: args})
		written, err := os.ReadFile(filepath.Join(workspace, "rows.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return res.Content, string(written)
	}

	t.Run("a region copied straight out of the read is an anchor", func(t *testing.T) {
		workspace, copied := setUp(t)
		if copied != "12|alice|admin" {
			t.Fatalf("the second line came back as %q; the fixture's own bytes are \"12|alice|admin\"", copied)
		}
		said, after := editRow(t, workspace, copied, "12|alice|owner")
		if after != intended {
			t.Errorf("copying a region into old_string did not produce what was asked for\nsaid: %s\nwant %q\ngot  %q", said, intended, after)
		}
	})

	// The same intent, through the instruction the bullet must never carry
	// again: the leading digits and separator are read as the tool's numbering
	// and dropped. Nothing refuses it — the remainder of a row is really in the
	// file — so the row is rewritten around a prefix that was content, and the
	// tool reports success.
	t.Run("dropping a leading number and separator writes something else", func(t *testing.T) {
		workspace, copied := setUp(t)
		stripped := regexp.MustCompile(`^\s*\d+[:|]\s?`).ReplaceAllString(copied, "")
		if stripped == copied {
			t.Fatalf("the copied line %q has no leading number to strip; this case no longer tests anything", copied)
		}
		said, after := editRow(t, workspace, stripped, "12|alice|owner")
		switch {
		case after == intended:
			t.Fatalf("stripping the line's leading characters produced the file the caller wanted, so an instruction to do it would be sound: %s", said)
		case after == original:
			// Refused, which is a safe way for the instruction to be wrong.
		default:
			// Accepted: the remainder of a row is really in the file, so what
			// was dropped as numbering is still there and the row now carries
			// it twice.
			if !strings.Contains(after, "12|12|alice|owner") {
				t.Errorf("the stripped anchor wrote something other than a row rebuilt around the prefix it dropped: %q (%s)", after, said)
			}
		}
	})
}

// Dropping the wrong advice is only half the job. These four are what keeps the
// read → edit loop working, and each one is now true of the code: the header
// says which lines came back, the bytes below it are the file's own, the anchor
// must be exact and unique, and whitespace is part of it.
func TestPromptFileNotesDescribeVerbatimReadsAndExactAnchors(t *testing.T) {
	prompt := filePrompt(t)
	for _, want := range []string{
		"lines <first>-<last> of <total>",
		"the file's exact bytes",
		"exact, unique old_string",
		"Preserve tabs and spaces exactly",
		"re-read the region you are editing",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt no longer says %q", want)
		}
	}
}

// A missed anchor is a stale or misremembered anchor; the bytes the model sent
// are not in the file, and no amount of extra context around them will find
// them. The old advice sent models into a retry loop that could not terminate,
// so the prompt has to name the real cause and the one move that works.
func TestPromptSendsAFailedMatchBackToTheFile(t *testing.T) {
	prompt := filePrompt(t)
	if !strings.Contains(prompt, "the anchor itself is wrong") {
		t.Errorf("prompt does not tell the model a failed match means a wrong anchor:\n%s", prompt)
	}
}

// claimAround returns the single sentence of the prompt that makes a claim
// mentioning needle. A claim has to be judged whole: an assertion that searches
// the entire prompt is satisfied by a true clause sitting next to a false one,
// which is how a sentence that scoped a translation to old_string alone stayed
// green while the tool was translating new_string too.
//
// It knows two sentence boundaries, ". " and a newline, which is enough for the
// tool notes as written and has two consequences for anyone rewording them. An
// abbreviation ("e.g.") ends the sentence early and fails the assertion loudly.
// A sentence ended with "!" or "?" does not end it at all: the claim merges with
// the sentence after it and can be satisfied by a qualifier that is no longer in
// the same sentence — a silent pass. Keep these bullets to plain full stops, or
// teach this function the terminator you introduce.
func claimAround(t *testing.T, prompt, needle string) string {
	t.Helper()
	at := strings.Index(prompt, needle)
	if at < 0 {
		t.Fatalf("prompt makes no claim mentioning %q", needle)
	}
	start := 0
	if i := strings.LastIndex(prompt[:at], ". "); i >= 0 {
		start = i + 2
	}
	if i := strings.LastIndex(prompt[:at], "\n"); i >= start {
		start = i + 1
	}
	end := len(prompt)
	if i := strings.Index(prompt[at:], ". "); i >= 0 {
		end = at + i + 1
	}
	if i := strings.Index(prompt[at:], "\n"); i >= 0 && at+i < end {
		end = at + i
	}
	return strings.TrimSpace(prompt[start:end])
}

// read_file appends a note of its own whenever it clips a read, so the lines
// below the header are not all file content — and the bullet making that claim
// is the same one that sends the model through large files with offset and
// limit, which is precisely when a note appears. Taken literally, an
// unqualified claim invites "… 2 more lines" into an anchor.
//
// Both branches that append a note are driven, because they do not produce the
// same shape. The note is written as "\n…" onto whatever the body ended with,
// so a blank line appears in front of it only when the body already ended in a
// newline of its own. On the byte-cap branch it does not, and that leading "\n"
// is the tool's rather than the file's.
func TestPromptExemptsTheToolsOwnTrailingNotesFromTheContentClaim(t *testing.T) {
	const noteMarker = "…"
	// maxReadBytes over in internal/tools. A file past it is truncated and noted.
	const byteCap = 400 * 1024
	oversized := strings.Repeat("x", byteCap+64)

	read, ok := tools.Default().Get("read_file")
	if !ok {
		t.Fatal("read_file is not registered")
	}

	// blankLineAlways is the verdict, and it starts as the answer that lets the
	// prompt promise most. A case that does not reach the end contributes no
	// evidence to it, so the count of cases that did is checked before the
	// verdict is used: otherwise a read whose shape changed takes the claim
	// check down with it and the prompt goes unexamined.
	blankLineAlways, judged := true, 0
	cases := []struct{ name, file, content, args, inTheFile, wantTail string }{
		{
			"clipped by line range",
			"notes.txt", "one\ntwo\nthree\nfour\nfive\n",
			`{"path":"notes.txt","offset":2,"limit":2}`,
			"two\nthree\n",
			"three\n\n… 2 more lines (use offset=4 to continue)\n",
		},
		{
			// One line, longer than the cap: no "more lines" note fires, and
			// the cap note lands straight onto a content byte. A minified
			// bundle, a single-line JSON document or a long-lined log.
			"clipped by the 400 KB byte cap",
			"bundle.min.js", oversized,
			`{"path":"bundle.min.js"}`,
			oversized[:byteCap],
			"x\n… file truncated at 400 KB\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, tc.file), []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			res := read.Execute(context.Background(), tools.Input{
				Workspace: workspace,
				Args:      []byte(tc.args),
			})
			if res.IsError {
				t.Fatalf("read_file: %s", res.Content)
			}
			if !strings.HasSuffix(res.Content, tc.wantTail) {
				t.Fatalf("read ends %q, want it to end %q — the prompt describes this shape and has to be revisited with it",
					lastBytes(res.Content, len(tc.wantTail)+8), tc.wantTail)
			}
			_, below, ok := strings.Cut(res.Content, "\n\n")
			if !ok {
				t.Fatalf("read_file output has no header line followed by a blank line: %q", lastBytes(res.Content, 120))
			}
			if !strings.HasPrefix(below, tc.inTheFile) {
				t.Fatalf("read_file did not return the range's own bytes first: %q", lastBytes(below, 120))
			}
			added := strings.TrimPrefix(below, tc.inTheFile)
			if strings.TrimSpace(added) == "" {
				t.Fatal("this branch no longer appends a note; the prompt's exception for one is stale and should go")
			}
			// The marker is the only property that holds on both branches, so
			// it is the only one the prompt may offer as the recognition rule.
			for _, line := range strings.Split(strings.Trim(added, "\n"), "\n") {
				if !strings.HasPrefix(line, noteMarker) {
					t.Fatalf("read_file adds a line the prompt gives the model no way to tell from content: %q", line)
				}
			}
			shown := strings.Split(strings.TrimSuffix(below, "\n"), "\n")
			for i, line := range shown {
				if !strings.HasPrefix(line, noteMarker) {
					continue
				}
				if i == 0 || shown[i-1] != "" {
					blankLineAlways = false
				}
				break
			}
			judged++
		})
	}
	if judged != len(cases) {
		t.Fatalf("%d of %d reads did not get as far as the note they append, so what read_file puts in front of one is unknown and the prompt's promise about it cannot be judged", len(cases)-judged, len(cases))
	}

	claim := claimAround(t, filePrompt(t), "that blank line")
	if !strings.Contains(claim, noteMarker) {
		t.Errorf("prompt claims file content below the header without exempting the %q notes read_file just appended: %q", noteMarker, claim)
	}
	// The prompt may describe what sits in front of a note only if every branch
	// puts it there. One does not, so promising it would tell the model the last
	// content line is terminated when the terminator it sees is the tool's.
	if !blankLineAlways {
		for _, promise := range []string{
			"preceded by a blank line", "after a blank line",
			"behind a blank line", "following a blank line",
		} {
			if strings.Contains(claim, promise) {
				t.Errorf("prompt says a note is %q, but a read above put one straight onto a content byte: %q", promise, claim)
			}
		}
	}
}

// lastBytes keeps a failure message readable when the subject is a 400 KB read.
func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// The recovery stage translates new_string because it had to translate
// old_string, so "written byte for byte" is false on exactly the path the rest
// of the same sentence describes. The tool is right to do it — the alternative
// is refusing an anchor it can match losslessly — so it is the sentence that
// has to change.
func TestPromptQualifiesTheByteForByteWriteTheRecoveryStageBreaks(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "win.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit, ok := tools.Default().Get("edit_file")
	if !ok {
		t.Fatal("edit_file is not registered")
	}
	// An all-LF anchor against a CRLF file: only the recovery stage can match it.
	res := edit.Execute(context.Background(), tools.Input{
		Workspace: workspace,
		Args:      []byte(`{"path":"win.txt","old_string":"beta\ngamma","new_string":"beta\nGAMMA"}`),
	})
	if res.IsError {
		t.Fatalf("edit_file: %s", res.Content)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "beta\nGAMMA") {
		t.Fatal("new_string reached disk byte for byte here; the prompt's exception for the recovery stage is stale and should go")
	}
	if want := "alpha\r\nbeta\r\nGAMMA\r\n"; string(after) != want {
		t.Fatalf("recovery stage wrote %q, want %q", after, want)
	}

	claim := claimAround(t, filePrompt(t), "byte for byte")
	if !strings.Contains(claim, "new_string's line breaks are expanded to CRLF") {
		t.Errorf("prompt promises a byte-for-byte write and does not name the translation edit_file just applied to new_string: %q", claim)
	}
}

// Nothing above is worth asserting if read_file has gone back to decorating
// lines: the prompt would be lying again, in the same way and to the same cost.
func TestPromptClaimOfVerbatimReadsHoldsAgainstTheRealTool(t *testing.T) {
	workspace := t.TempDir()
	original := "| Date | Event |\n|---|---|\n\tindented\n"
	if err := os.WriteFile(filepath.Join(workspace, "table.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	read, ok := tools.Default().Get("read_file")
	if !ok {
		t.Fatal("read_file is not registered")
	}
	res := read.Execute(context.Background(), tools.Input{
		Workspace: workspace,
		Args:      []byte(`{"path":"table.md"}`),
	})
	if res.IsError {
		t.Fatalf("read_file: %s", res.Content)
	}
	header, body, ok := strings.Cut(res.Content, "\n\n")
	if !ok {
		t.Fatalf("read_file output has no header line followed by a blank line: %q", res.Content)
	}
	if header != "table.md — lines 1-3 of 3" {
		t.Errorf("header is not the shape the prompt describes: %q", header)
	}
	if body != original {
		t.Errorf("prompt promises the file's exact bytes, read_file returned %q", body)
	}
}
