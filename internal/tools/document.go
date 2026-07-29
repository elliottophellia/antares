package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/ledongthuc/pdf"
)

// UploadsDir is the temporary root for attached files. It lives under the
// antares home (not the workspace), so uploads never pollute the user's project
// and can be cleaned per session or by age.
func UploadsDir() string { return config.Path("uploads") }

// readDocumentTool extracts the text out of a document so any model can read it
// — including ones with no vision or file support. It handles PDF and DOCX
// natively (pure Go, no external tools), and reads text-based files directly.
// This is what makes an uploaded attachment usable: the file lands in a temp
// dir and the agent calls this to pull its text into the conversation.
type readDocumentTool struct{}

func (readDocumentTool) Name() string { return "read_document" }

func (readDocumentTool) Description() string {
	return "Extract the text from a document so you can read it: PDF (.pdf), Word (.docx), and any " +
		"text-based file (.txt, .md, .csv, .json, code, etc.). Use this for user-attached files, which " +
		"arrive under the uploads directory. Returns the extracted text. Binary formats other than PDF/DOCX " +
		"are not supported."
}

func (readDocumentTool) Schema() map[string]any {
	return schema(map[string]any{
		"path": prop("string", "Path to the document. An attachment path (e.g. uploads/<file>) or a workspace-relative path."),
	}, "path")
}

// maxDocChars bounds how much extracted text is returned, so a huge document
// cannot blow up the context in one call.
const maxDocChars = 200_000

func (readDocumentTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path string `json:"path"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return Errorf("path is required")
	}

	path, err := resolveDocPath(in.Workspace, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	if fi.IsDir() {
		return Errorf("%s is a directory", args.Path)
	}

	var text string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		text, err = extractPDF(path)
	case ".docx":
		text, err = extractDOCX(path)
	default:
		// Everything else is treated as text; reject if it is not valid UTF-8.
		var data []byte
		data, err = os.ReadFile(path)
		if err == nil {
			if !isProbablyText(data) {
				return Errorf("%s is not a supported document (only PDF, DOCX, and text files can be extracted)", args.Path)
			}
			text = string(data)
		}
	}
	if err != nil {
		return Errorf("could not extract %s: %v", args.Path, err)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return Text(fmt.Sprintf("%s has no extractable text.", filepath.Base(path)))
	}
	truncated := false
	if len(text) > maxDocChars {
		text = text[:maxDocChars]
		truncated = true
	}
	out := fmt.Sprintf("Extracted text from %s:\n\n%s", filepath.Base(path), text)
	if truncated {
		out += "\n\n[truncated: document is longer than the limit]"
	}
	return Text(out)
}

// resolveDocPath allows an attachment (under the uploads dir) or a
// workspace-relative path, and blocks traversal out of either root.
func resolveDocPath(workspace, p string) (string, error) {
	clean := filepath.Clean(p)
	// Absolute paths are only allowed if they already sit under a known root.
	uploads := UploadsDir()
	roots := []string{uploads, workspace}
	if filepath.IsAbs(clean) {
		for _, root := range roots {
			if root != "" && within(root, clean) {
				return clean, nil
			}
		}
		return "", fmt.Errorf("path is outside the allowed directories")
	}
	// Relative: try uploads first (attachments), then the workspace.
	for _, root := range roots {
		if root == "" {
			continue
		}
		cand := filepath.Join(root, clean)
		if within(root, cand) {
			if _, err := os.Stat(cand); err == nil {
				return cand, nil
			}
		}
	}
	// Default to workspace resolution so the error names a sensible path.
	if workspace != "" {
		return resolvePath(workspace, p)
	}
	return filepath.Join(uploads, clean), nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// extractPDF pulls the plain text out of every page of a PDF.
func extractPDF(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	reader, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(&buf, reader); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var xmlTag = regexp.MustCompile(`<[^>]+>`)
var docxParaBreak = regexp.MustCompile(`</w:p>`)

// extractDOCX reads word/document.xml from the .docx (a zip) and strips the XML
// to text, turning paragraph breaks into newlines. Good enough to feed a model
// without pulling in a full OOXML parser.
func extractDOCX(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.Name != "word/document.xml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		// Preserve paragraph boundaries, then drop every remaining tag.
		s := docxParaBreak.ReplaceAllString(string(data), "\n")
		s = xmlTag.ReplaceAllString(s, "")
		return s, nil
	}
	return "", fmt.Errorf("not a valid .docx (no word/document.xml)")
}

// isProbablyText reports whether data looks like UTF-8 text rather than binary.
func isProbablyText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return false // NUL byte → binary
	}
	return true
}
