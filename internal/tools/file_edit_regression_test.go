package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAndEditPreserveTabbedCRLFContent(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.cpp")
	original := "if (ready) {\r\n\treturn !failed;\r\n}\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Workspace: workspace, Args: []byte(`{"path":"sample.cpp"}`)}
	read := (readFileTool{}).Execute(context.Background(), in)
	if read.IsError {
		t.Fatalf("read_file: %s", read.Content)
	}
	if !strings.Contains(read.Content, "\treturn !failed;\r\n") {
		t.Fatalf("read output does not preserve the line's tab and CRLF: %q", read.Content)
	}

	edit := (editFileTool{}).Execute(context.Background(), Input{
		Workspace: workspace,
		Args:      []byte(`{"path":"sample.cpp","old_string":"if (ready) {\n\treturn !failed;\n}","new_string":"if (ready) {\n\treturn success;\n}"}`),
	})
	if edit.IsError {
		t.Fatalf("edit_file: %s", edit.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "if (ready) {\r\n\treturn success;\r\n}\r\n"
	if string(got) != want {
		t.Fatalf("edited bytes = %q, want %q", got, want)
	}
}

// A model writes \n for a line break whatever the file it read uses, so an
// old_string copied out of a CRLF file commonly comes back with LF. edit_file
// must still match it and preserve the file's original line endings on write.
func TestEditFileMatchesCRLFWhenCopiedFromRead(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "win.go")
	original := "package main\r\n\r\nfunc main() {\r\n\tfmt.Println(\"hi\")\r\n}\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	readArgs, _ := json.Marshal(map[string]any{"path": "win.go"})
	read := (readFileTool{}).Execute(context.Background(), Input{Args: readArgs, Workspace: workspace})
	if read.IsError {
		t.Fatalf("read: %s", read.Content)
	}

	body := readFileBody(t, read.Content)
	if body != original {
		t.Fatalf("read_file returned something other than the file's bytes: %q", body)
	}
	copied := strings.Split(strings.TrimSuffix(body, "\r\n"), "\r\n")
	// Function body as the model reassembles it, with LF for every break.
	oldString := strings.Join(copied[2:5], "\n")
	newString := strings.Replace(oldString, "hi", "bye", 1)

	editArgs, _ := json.Marshal(map[string]any{
		"path": "win.go", "old_string": oldString, "new_string": newString,
	})
	edited := (editFileTool{}).Execute(context.Background(), Input{Args: editArgs, Workspace: workspace})
	if edited.IsError {
		t.Fatalf("edit_file failed for CRLF file after read_file copy: %s", edited.Content)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\r\n\r\nfunc main() {\r\n\tfmt.Println(\"bye\")\r\n}\r\n"
	if string(got) != want {
		t.Fatalf("edited content = %q\nwant %q", got, want)
	}
}

// When the match still fails, the error must say what went wrong in a way the
// model can act on (tabs vs spaces is the common indentation trap).
func TestEditFileDiagnosesTabVsSpaceMismatch(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "tabs.c")
	original := "\t\tif (x) {\n\t\t\tdo_work();\n\t\t}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	editArgs, _ := json.Marshal(map[string]any{
		"path":       "tabs.c",
		"old_string": "  if (x) {\n    do_work();\n  }",
		"new_string": "  if (x) {\n    do_work2();\n  }",
	})
	edited := (editFileTool{}).Execute(context.Background(), Input{Args: editArgs, Workspace: workspace})
	if !edited.IsError {
		t.Fatal("expected failure for tab/space mismatch")
	}
	if !strings.Contains(edited.Content, "tab") {
		t.Fatalf("error should diagnose tabs vs spaces, got: %s", edited.Content)
	}
}

func TestEditFileAmbiguousListsCurrentMatchLines(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "repeat.go")
	content := "func a() {\n\treturn value\n}\nfunc b() {\n\treturn value\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "repeat.go", "old_string": "\treturn value", "new_string": "\treturn other"})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "Current match line(s): 2, 5") {
		t.Fatalf("unexpected ambiguity result: %+v", result)
	}
}

func TestEditFileNotFoundShowsNearMiss(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "names.go")
	if err := os.WriteFile(path, []byte("func attachEntity() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "names.go", "old_string": "func attachEntit() {}", "new_string": "func attachEntity2() {}"})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "attachEntity") {
		t.Fatalf("near-miss missing from result: %+v", result)
	}
}

// The hint used to skip any token containing "read_file", from the era when a
// stale anchor could be a line of read_file's own NUMBER| output and the token
// meant "this came from the tool, not the file". There is no such output any
// more, so the exclusion only fires on what it was never about: source that
// has a read_file identifier in it, which here is most of the file tools' own
// code and their callers.
func TestEditFileNearMissHintDoesNotSkipReadFileIdentifiers(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "dispatch.go")
	content := "func dispatch(name string) {\n    if name == \"read_file\" && verbose {\n        log(name)\n    }\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"path":       "dispatch.go",
		"old_string": "    if name == \"read_file\" && verbos {",
		"new_string": "    if name == \"read_file\" {",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError {
		t.Fatalf("an anchor that is not in the file was accepted: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Near-miss") {
		t.Errorf("no near-miss hint for an anchor whose only long token is read_file: %s", result.Content)
	}
	if !strings.Contains(result.Content, "line 2:") {
		t.Errorf("hint does not name the line that shares the token: %s", result.Content)
	}
}

// The row in the file says "85 (early stop @55)" and the anchor abbreviates it
// to "85 (ES@55)", so the anchor is not in the file. This is the case the
// adjacent-insertion recovery was built for, and the case that shows why it
// cannot exist: the same shape is indistinguishable from an anchor whose row
// was deleted, where the insertion lands against a sibling row instead. A
// stale anchor is a stale read, and the answer is to read again.
func TestEditFileRefusesAnAbbreviatedTableRowAnchor(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "README.md")
	actual := "| **pool39v2** | 14 hand + 25 vision-audited | 33 train / 6 val | T4 | 85 (early stop @55) | **0.009** | `artifacts/pool39v2/` |\n"
	if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "| **pool39v2** | 14 hand + 25 vision-audited | 33 train / 6 val | T4 | 85 (ES@55) | **0.009** | `artifacts/pool39v2/` |"
	newString := old + "\n| **pool39v2_sc** | single-class icon | 33 train / 6 val | T4 | 70 | **0.519** | `artifacts/pool39v2_sc/` |"
	args, _ := json.Marshal(map[string]any{"path": "README.md", "old_string": old, "new_string": newString})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "old_string not found") {
		t.Fatalf("an anchor that is not in the file was accepted: %+v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != actual {
		t.Fatalf("file changed although the anchor is absent:\n%s", got)
	}
}

// One space apart from a line that is really there is still not that line. The
// margin between "close" and "correct" is where silent corruption lives, and
// nothing in the tool is allowed to cross it.
func TestEditFileRefusesAnAnchorOneSpaceOffARealLine(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "f.txt")
	content := "start\nreturn nil // TODO cleanup\nmiddle\nreturn nil\nend\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "return  nil" // double space; the file has one
	args, _ := json.Marshal(map[string]any{
		"path": "f.txt", "old_string": old, "new_string": old + "\nINSERTED",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError {
		t.Fatalf("a near-miss anchor was accepted: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("file changed although the anchor is absent:\n%s", got)
	}
}

// replace_all promises "replace every exact occurrence", so an anchor that
// occurs zero times must change nothing at all.
func TestEditFileReplaceAllRefusesAnAbsentAnchor(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "ra.txt")
	content := "return nil // TODO cleanup\nmiddle\nreturn nil\nend\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "return  nil"
	args, _ := json.Marshal(map[string]any{
		"path": "ra.txt", "old_string": old, "new_string": old + "\nINSERTED", "replace_all": true,
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError {
		t.Fatalf("replace_all accepted an anchor that occurs zero times: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("file changed although the anchor is absent:\n%s", got)
	}
}

// A file with mixed line endings must never reject an old_string whose bytes
// match the file exactly. (fileEOL used to pick CRLF because one line used it,
// then converted the LF old_string so it matched nothing.)
func TestEditFileExactMatchOnMixedEOLFile(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "mixed.txt")
	content := "alpha\r\nbeta\nGAMMA\ndelta\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"path": "mixed.txt", "old_string": "beta\nGAMMA", "new_string": "beta\nGAMMA2",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("exact byte match rejected on mixed-EOL file: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\r\nbeta\nGAMMA2\ndelta\n"
	if string(got) != want {
		t.Fatalf("edited = %q, want %q", got, want)
	}
}

// One stray lone CR byte anywhere in an LF file used to flip fileEOL to "\r"
// and permanently break every multi-line edit in that file.
func TestEditFileExactMatchDespiteStrayCR(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "cr.txt")
	content := "one\ntwo\nnote ends\rrest\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"path": "cr.txt", "old_string": "four\nfive", "new_string": "four\nFIVE",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("stray CR poisoned an exact match: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "one\ntwo\nnote ends\rrest\nfour\nFIVE\n"
	if string(got) != want {
		t.Fatalf("edited = %q, want %q", got, want)
	}
}

// Truncating at the byte cap must not cut a multi-byte rune in half and then
// misreport the whole file as binary.
func TestReadFileTruncationDoesNotSplitRune(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "big.txt")
	// "é" is 2 bytes; place it so the maxReadBytes cut lands inside it.
	content := strings.Repeat("a", maxReadBytes-1) + "é" + strings.Repeat("b", 16)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Workspace: workspace, Args: []byte(`{"path":"big.txt"}`)}
	result := (readFileTool{}).Execute(context.Background(), in)
	if result.IsError {
		t.Fatalf("truncated UTF-8 file misread as binary: %s", result.Content)
	}
	if !strings.Contains(result.Content, "file truncated") {
		t.Fatalf("missing truncation notice: %s", result.Content)
	}
}

// Classic-Mac style lone CR line endings terminate lines, so such a file is
// three lines rather than one — but counting them is no licence to rewrite
// them, and edit_file matches the CR bytes that are really there.
func TestReadFileCountsLoneCRLinesWithoutRewritingThem(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "old.txt")
	original := "a\rb\rc"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Workspace: workspace, Args: []byte(`{"path":"old.txt"}`)}
	result := (readFileTool{}).Execute(context.Background(), in)
	if result.IsError {
		t.Fatalf("read failed: %s", result.Content)
	}
	if !strings.HasPrefix(result.Content, "old.txt — lines 1-3 of 3\n\n") {
		t.Fatalf("lone-CR file not counted as three lines: %q", result.Content)
	}
	if body := readFileBody(t, result.Content); body != original {
		t.Fatalf("lone-CR bytes rewritten for display: %q, want %q", body, original)
	}
}

// A markdown table row is the shape that made "close enough" look reasonable,
// and two sibling rows are the shape that made it dangerous: the anchor is
// equally near to both, and either choice writes into a row the caller did not
// name.
func TestEditFileRefusesAnAnchorNearTwoSiblingRows(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "README.md")
	content := "| **pool39v2** | 85 (early stop @55) | artifacts/a |\n| **pool39v2** | 85 (early stop @55) | artifacts/b |\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "| **pool39v2** | 85 (ES@55) | artifacts/c |"
	args, _ := json.Marshal(map[string]any{
		"path": "README.md", "old_string": old, "new_string": old + "\n| new row |",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "old_string not found") {
		t.Fatalf("an anchor that is not in the file was accepted: %+v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("file changed although the anchor is absent: %q", got)
	}
}
