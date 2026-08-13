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
