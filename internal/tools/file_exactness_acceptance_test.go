package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These four cases drive the real edit_file tool against real files. Each one
// was observed writing to disk and reporting success against an anchor that is
// not in the file, or writing something other than what it was given. They are
// the acceptance criteria for restoring the two properties edit_file lost:
// write only on an exact match, and write exactly new_string.

func editOnDisk(t *testing.T, name, original string, args map[string]any) (said string, isError bool, after string) {
	t.Helper()
	workspace := t.TempDir()
	path := filepath.Join(workspace, name)
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res := (editFileTool{}).Execute(context.Background(), Input{Args: raw, Workspace: workspace})
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return res.Content, res.IsError, string(written)
}

// A space-indented anchor against a tab-indented file is the most common way an
// edit goes wrong, and the anchor is nowhere in the file. Splicing it in
// produced Python that fails to compile with TabError.
func TestEditRefusesAnAnchorThatIsNotInTheFile(t *testing.T) {
	original := "def main():\n\tif enabled:\n\t\trun()\n\t\treturn 0\n"
	said, isError, after := editOnDisk(t, "app.py", original, map[string]any{
		"path":       "app.py",
		"old_string": "    if enabled:",
		"new_string": "    if enabled:\n        setup()",
	})
	if after != original {
		t.Errorf("file changed though old_string is absent\nsaid: %s\ngot:  %q", said, after)
	}
	if !isError {
		t.Errorf("reported success for an anchor that does not exist: %s", said)
	}
}

// new_string is authored by the model and never copied out of read_file, so a
// leading NUMBER| in it is content, not a prefix to strip.
func TestEditWritesNewStringVerbatim(t *testing.T) {
	original := "1|alice|admin\n2|bob|user\n"
	said, isError, after := editOnDisk(t, "rows.psv", original, map[string]any{
		"path":       "rows.psv",
		"old_string": "2|bob|user",
		"new_string": "2|bob|admin",
	})
	if isError {
		t.Fatalf("exact anchor was refused: %s", said)
	}
	if want := "1|alice|admin\n2|bob|admin\n"; after != want {
		t.Errorf("new_string was rewritten before it landed\nwant %q\ngot  %q", want, after)
	}
}

// replace_all means every exact occurrence. An anchor occurring zero times must
// change nothing, however it might otherwise be rewritten to match.
func TestEditReplaceAllNeedsExactOccurrences(t *testing.T) {
	original := "1|pending\n2|pending\n3|pending\n"
	said, _, after := editOnDisk(t, "queue.psv", original, map[string]any{
		"path":        "queue.psv",
		"old_string":  "9|pending",
		"new_string":  "9|done",
		"replace_all": true,
	})
	if after != original {
		t.Errorf("an anchor matching zero times rewrote the file\nsaid: %s\ngot:  %q", said, after)
	}
}

// An error has to describe the string the caller sent. Reporting occurrences of
// a silently rewritten anchor sends the model into a retry that cannot succeed.
func TestEditErrorDescribesTheCallersAnchor(t *testing.T) {
	original := "1|pending\n2|pending\n3|pending\n"
	said, isError, _ := editOnDisk(t, "queue.psv", original, map[string]any{
		"path":       "queue.psv",
		"old_string": "9|pending",
		"new_string": "9|done",
	})
	if !isError {
		t.Fatalf("expected an error for an anchor appearing zero times: %s", said)
	}
	if strings.Contains(said, "appears 3 times") {
		t.Errorf("error counts occurrences of a string the caller never sent: %s", said)
	}
}
