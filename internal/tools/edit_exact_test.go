package tools

import (
	"strings"
	"testing"
)

// The acceptance tests next door pin that edit_file refuses an anchor the file
// does not contain. These pin the other half of the contract: what reaches disk
// when it does write, and what the tool says it did. Both drive the real tool
// against a real file, through editOnDisk.

// On the stage where old_string matched the file's own bytes there is nothing
// to reconcile, so anything done to new_string on the way to disk is damage.
// A CR inside a value is data, not a line break; rewriting it hands the file a
// line the caller never wrote.
func TestEditWritesNewStringByteForByteOnTheExactStage(t *testing.T) {
	original := "id\tvalue\nMARKER\ntail\n"
	said, isError, after := editOnDisk(t, "rows.tsv", original, map[string]any{
		"path":       "rows.tsv",
		"old_string": "MARKER",
		"new_string": "note ends\rand continues",
	})
	if isError {
		t.Fatalf("an anchor present in the file was refused: %s", said)
	}
	if want := "id\tvalue\nnote ends\rand continues\ntail\n"; after != want {
		t.Errorf("new_string was rewritten on the way to disk\nwant %q\ngot  %q", want, after)
	}
}

// A model writes \n for a line break whatever the file it read used, so an
// all-LF anchor against a CRLF file is a well-defined ambiguity rather than a
// guess — the one translation the tool is allowed to make. What it must not do
// is make it silently: the caller asked for particular bytes and is entitled to
// know that different ones were matched and written.
func TestEditTranslatesAnLFAnchorForACRLFFileAndSaysSo(t *testing.T) {
	original := "package main\r\n\r\nfunc main() {\r\n\tfmt.Println(\"hi\")\r\n}\r\n"
	said, isError, after := editOnDisk(t, "win.go", original, map[string]any{
		"path":       "win.go",
		"old_string": "func main() {\n\tfmt.Println(\"hi\")\n}",
		"new_string": "func main() {\n\tfmt.Println(\"bye\")\n}",
	})
	if isError {
		t.Fatalf("an LF anchor was refused against a CRLF file: %s", said)
	}
	want := "package main\r\n\r\nfunc main() {\r\n\tfmt.Println(\"bye\")\r\n}\r\n"
	if after != want {
		t.Errorf("new_string did not land in the file's own line endings\nwant %q\ngot  %q", want, after)
	}
	if !strings.Contains(said, "LF to CRLF") {
		t.Errorf("success message does not disclose the translation it performed: %s", said)
	}
}

// "Matched exactly" and "matched after a translation" are different facts, and
// a caller deciding whether to re-read acts on them differently. The exact path
// has nothing to disclose, so it must not decorate its result.
func TestEditReportsNoRecoveryWhenTheAnchorMatchedExactly(t *testing.T) {
	original := "alpha\r\nbeta\r\ngamma\r\n"
	said, isError, after := editOnDisk(t, "crlf.txt", original, map[string]any{
		"path":       "crlf.txt",
		"old_string": "beta\r\ngamma",
		"new_string": "beta\r\nGAMMA",
	})
	if isError {
		t.Fatalf("a byte-exact anchor was refused: %s", said)
	}
	if want := "alpha\r\nbeta\r\nGAMMA\r\n"; after != want {
		t.Errorf("edited bytes = %q, want %q", after, want)
	}
	if strings.Contains(said, "[") {
		t.Errorf("an exact match reported a recovery: %s", said)
	}
}

// A space-indented anchor against a tab-indented file is the commonest way an
// edit misses, and the tool already knows how to say so. The message was
// unreachable: the adjacent-insertion splice claimed the edit first and wrote
// the spaces into the file.
func TestEditTabVersusSpaceAnchorGetsTheTabDiagnostic(t *testing.T) {
	original := "def main():\n\tif enabled:\n\t\trun()\n"
	said, isError, after := editOnDisk(t, "app.py", original, map[string]any{
		"path":       "app.py",
		"old_string": "    if enabled:",
		"new_string": "    if enabled:\n        setup()",
	})
	if !isError {
		t.Fatalf("an anchor that is not in the file was accepted: %s", said)
	}
	if after != original {
		t.Errorf("file changed although the edit failed: %q", after)
	}
	if !strings.Contains(said, "indents with TAB characters") {
		t.Errorf("error does not name the tab-versus-space mismatch: %s", said)
	}
}
