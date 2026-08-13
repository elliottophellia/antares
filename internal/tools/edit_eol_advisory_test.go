package tools

import (
	"strings"
	"testing"
)

// An anchor that matched the file's own bytes authorises no rewriting of the
// replacement, so LF breaks in new_string reach a CRLF file as LF. That is the
// right trade — every byte asked for is on disk — but it leaves the file with
// two line-ending flavors, and the caller is the only one who can decide
// whether that matters. Saying so costs nothing and is the difference between
// a visible consequence and a silent one.
func TestEditNotesLFReplacementLandingInACRLFFile(t *testing.T) {
	said, isError, after := editOnDisk(t, "win.txt", "alpha\r\nbeta\r\ngamma\r\n", map[string]any{
		"path":       "win.txt",
		"old_string": "beta\r\ngamma",
		"new_string": "beta\nGAMMA",
	})
	if isError {
		t.Fatalf("a byte-exact anchor was refused: %s", said)
	}
	if want := "alpha\r\nbeta\nGAMMA\r\n"; after != want {
		t.Fatalf("new_string was not written verbatim\nwant %q\ngot  %q", want, after)
	}
	for _, want := range []string{"CRLF line endings", "LF line breaks"} {
		if !strings.Contains(said, want) {
			t.Errorf("success message does not mention %q: %s", want, said)
		}
	}
}

// A replacement that mixes the two flavors leaves the file exactly as
// inconsistent as an all-LF one, and hides it better: the model wrote most of
// its breaks the way the file has them, so the one it did not is the easiest
// to miss. Asking whether new_string contains a CRLF anywhere answers a
// different question and skips the note here.
func TestEditNotesAReplacementThatMixesItsOwnLineEndings(t *testing.T) {
	said, isError, after := editOnDisk(t, "win.txt", "alpha\r\nbeta\r\ngamma\r\n", map[string]any{
		"path":       "win.txt",
		"old_string": "beta\r\ngamma",
		"new_string": "one\r\ntwo\nthree",
	})
	if isError {
		t.Fatalf("a byte-exact anchor was refused: %s", said)
	}
	if want := "alpha\r\none\r\ntwo\nthree\r\n"; after != want {
		t.Fatalf("new_string was not written verbatim\nwant %q\ngot  %q", want, after)
	}
	for _, want := range []string{"CRLF line endings", "LF line breaks"} {
		if !strings.Contains(said, want) {
			t.Errorf("a bare LF landed in a CRLF file with no mention of %q: %s", want, said)
		}
	}
}

// The note describes one situation, so it must appear in exactly that one. On
// every other path it is either false or noise, and a note the caller learns to
// ignore is worse than no note at all.
func TestEditDoesNotNoteLineEndingsOnAnyOtherPath(t *testing.T) {
	for _, tc := range []struct {
		name, original, oldString, newString, want string
	}{
		{
			"an LF file has nothing to be inconsistent with",
			"alpha\nbeta\ngamma\n", "beta\ngamma", "beta\nGAMMA",
			"alpha\nbeta\nGAMMA\n",
		},
		{
			"a replacement written in the file's own endings is consistent",
			"alpha\r\nbeta\r\ngamma\r\n", "beta\r\ngamma", "beta\r\nGAMMA",
			"alpha\r\nbeta\r\nGAMMA\r\n",
		},
		{
			"a replacement with no line break has no line ending to differ in",
			"alpha\r\nbeta\r\ngamma\r\n", "beta", "BETA",
			"alpha\r\nBETA\r\ngamma\r\n",
		},
		{
			// The recovery stage translates new_string because it had to
			// translate old_string, so CRLF is what actually reaches the file.
			"the LF-to-CRLF recovery already wrote CRLF",
			"alpha\r\nbeta\r\ngamma\r\n", "beta\ngamma", "beta\nGAMMA",
			"alpha\r\nbeta\r\nGAMMA\r\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			said, isError, after := editOnDisk(t, "f.txt", tc.original, map[string]any{
				"path":       "f.txt",
				"old_string": tc.oldString,
				"new_string": tc.newString,
			})
			if isError {
				t.Fatalf("edit refused: %s", said)
			}
			if after != tc.want {
				t.Fatalf("edited bytes = %q, want %q", after, tc.want)
			}
			if strings.Contains(said, "Note:") {
				t.Errorf("line-ending note appeared where it does not apply: %s", said)
			}
		})
	}
}
