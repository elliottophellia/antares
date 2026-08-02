package tools

import (
	"context"
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
	if !strings.Contains(read.Content, "2|\treturn !failed;") {
		t.Fatalf("read output does not preserve indentation unambiguously: %q", read.Content)
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
