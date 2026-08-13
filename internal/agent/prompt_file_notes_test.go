package agent

import (
	"context"
	"os"
	"path/filepath"
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
// standing order to corrupt data. read_file adds no prefix to a line, and
// new_string is authored rather than copied, so telling the model to keep only
// what follows a "|" deletes real content from files whose lines start with one.
func TestPromptFileNotesDoNotDescribeALineNumberPrefix(t *testing.T) {
	prompt := filePrompt(t)
	for _, gone := range []string{
		"NUMBER|",
		"NUMBER|CONTENT",
		"metadata only",
		"content after `|`",
		"never the line number",
		"Line endings are matched automatically",
	} {
		if strings.Contains(prompt, gone) {
			t.Errorf("prompt still describes the removed line-prefix format: %q", gone)
		}
	}
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
// limit, which is precisely when the note appears. Taken literally, an
// unqualified claim invites "… 2 more lines" into an anchor.
func TestPromptExemptsTheToolsOwnTrailingNoteFromTheContentClaim(t *testing.T) {
	const noteMarker = "…"

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read, ok := tools.Default().Get("read_file")
	if !ok {
		t.Fatal("read_file is not registered")
	}
	res := read.Execute(context.Background(), tools.Input{
		Workspace: workspace,
		Args:      []byte(`{"path":"notes.txt","offset":2,"limit":2}`),
	})
	if res.IsError {
		t.Fatalf("read_file: %s", res.Content)
	}
	_, below, ok := strings.Cut(res.Content, "\n\n")
	if !ok {
		t.Fatalf("read_file output has no header line followed by a blank line: %q", res.Content)
	}
	const inTheFile = "two\nthree\n"
	if !strings.HasPrefix(below, inTheFile) {
		t.Fatalf("read_file did not return the range's own bytes first: %q", below)
	}
	added := strings.TrimPrefix(below, inTheFile)
	if strings.TrimSpace(added) == "" {
		t.Fatal("a clipped read no longer appends a note; the prompt's exception for one is stale and should go")
	}
	for _, line := range strings.Split(strings.Trim(added, "\n"), "\n") {
		if !strings.HasPrefix(line, noteMarker) {
			t.Fatalf("read_file appends a line the prompt gives the model no way to tell from content: %q", line)
		}
	}

	claim := claimAround(t, filePrompt(t), "that blank line")
	if !strings.Contains(claim, noteMarker) {
		t.Errorf("prompt claims file content below the header without exempting the %q note read_file just appended: %q", noteMarker, claim)
	}
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
