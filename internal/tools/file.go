package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxReadBytes caps a single read so a stray large file cannot blow the context.
const maxReadBytes = 400 * 1024

// resolvePath joins a user-supplied path against the workspace and refuses to
// escape it, which is the sandbox boundary for every file tool. It is the
// confined form: an ordinary (non-project) session uses it for both reads and
// writes. Project sessions use resolveRead / resolveWrite instead.
func resolvePath(workspace, p string) (string, error) {
	clean, err := cleanPath(workspace, p)
	if err != nil {
		return "", err
	}
	if withinRoot(workspace, clean) {
		return clean, nil
	}
	return "", fmt.Errorf("path %q is outside the workspace (%s)", p, workspace)
}

// cleanPath expands ~, joins a relative path onto workspace, and cleans it,
// without any boundary check.
func cleanPath(workspace, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	return filepath.Clean(p), nil
}

// within reports whether clean resolves inside root (through symlinks).
func withinRoot(root, clean string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(r); err == nil {
		r = real
	}
	// A write target frequently does not exist yet (creating a new file, or a
	// file in a new directory), so EvalSymlinks on the full path would fail and
	// leave symlinks in the parent unresolved — on macOS the temp/root dirs are
	// themselves symlinks, so an unresolved target then looks "outside" a
	// resolved root. Resolve the deepest existing ancestor instead and re-append
	// the missing tail, so the comparison is symlink-correct either way.
	abs = evalExisting(abs)
	rel, err := filepath.Rel(r, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// evalExisting resolves symlinks over the longest existing prefix of p and
// re-joins the not-yet-created tail, so paths that point at files or dirs that
// do not exist yet still compare correctly against a symlink-resolved root.
func evalExisting(p string) string {
	tail := ""
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, tail)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the root without finding an existing ancestor.
			return p
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

// resolveRead resolves a path for a READ. In a project session (WriteRoots set)
// reads are allowed anywhere on the machine, so the agent can read and copy
// files from outside the project. In an ordinary session reads stay confined to
// the workspace, exactly as before.
func resolveRead(in Input, p string) (string, error) {
	if len(in.WriteRoots) == 0 {
		return resolvePath(in.Workspace, p)
	}
	return cleanPath(in.Workspace, p)
}

// resolveWrite resolves a path for a WRITE and confines it to the allowed roots.
// An ordinary session confines to the workspace; a project session confines to
// its WriteRoots (the project folder plus the antares workspace). Writing
// anywhere else is refused — this is a hard boundary the model cannot cross.
func resolveWrite(in Input, p string) (string, error) {
	if len(in.WriteRoots) == 0 {
		return resolvePath(in.Workspace, p)
	}
	clean, err := cleanPath(in.Workspace, p)
	if err != nil {
		return "", err
	}
	for _, root := range in.WriteRoots {
		if strings.TrimSpace(root) != "" && withinRoot(root, clean) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("path %q is outside this project — writes are only allowed inside %s (reads and copies from elsewhere are fine)",
		p, strings.Join(in.WriteRoots, ", "))
}

func relTo(workspace, p string) string {
	if rel, err := filepath.Rel(workspace, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// ---- read_file --------------------------------------------------------------

type readFileTool struct{}

func (readFileTool) Name() string { return "read_file" }
func (readFileTool) Description() string {
	return "Read a text file from the workspace. Returns a header line naming the path and line range, a blank line, then the file's exact bytes — copy any region of it straight into edit_file.old_string. Use offset/limit for large files."
}
func (readFileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":   prop("string", "File path, relative to the workspace or absolute inside it."),
		"offset": propDefault("integer", "1-indexed line to start from.", 1),
		"limit":  propDefault("integer", "Maximum number of lines to return.", 2000),
	}, "path")
}

func (readFileTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	path, err := resolveRead(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	if fi.IsDir() {
		return Errorf("%s is a directory; use list_files instead", args.Path)
	}

	f, err := os.Open(path)
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	defer f.Close()
	// The cap has to bound the read, not trim what has already been read. The
	// size stat reports cannot bound it either: a character device, most of
	// /proc, and a file being appended to during the read all yield more than
	// stat promised, and /dev/zero reports zero bytes and never ends. Reading
	// one byte past the cap is what tells a file at the cap from one over it.
	data, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	truncatedBytes := false
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
		truncatedBytes = true
		// The cut can land inside a multi-byte rune; trimming up to three
		// trailing bytes keeps a genuine text file from reading as binary.
		for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) {
		return Errorf("%s appears to be a binary file (%d bytes)", args.Path, fi.Size())
	}

	content := string(data)
	lines := lineSpans(content)
	offset := args.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 2000
	}
	start := offset - 1
	// An empty file has no line 1 to be past, so offset 1 on it reads as
	// "nothing here" rather than as a mistake.
	if start > 0 && start >= len(lines) {
		return Errorf("offset %d is past end of file (%d lines)", offset, len(lines))
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	// The selected lines' own bytes, terminators included, so what the model is
	// shown is what edit_file can find. Slicing to the start of the line after
	// the range keeps the last terminator; nothing here rewrites tabs, CRLF or
	// a lone CR.
	body := ""
	if start < end {
		to := len(content)
		if end < len(lines) {
			to = lines[end].start
		}
		body = content[lines[start].start:to]
	}
	// An empty file has no line 1, so the header names no line rather than
	// inventing one.
	first := start + 1
	if start == end {
		first = 0
	}

	rel := relTo(in.Workspace, path)
	var b strings.Builder
	fmt.Fprintf(&b, "%s — lines %d-%d of %d\n\n", rel, first, end, len(lines))
	b.WriteString(body)
	if end < len(lines) {
		fmt.Fprintf(&b, "\n… %d more lines (use offset=%d to continue)\n", len(lines)-end, end+1)
	}
	if truncatedBytes {
		b.WriteString("\n… file truncated at 400 KB\n")
	}
	return Result{Content: b.String(), Meta: map[string]any{
		"path":        rel,
		"first_line":  first,
		"last_line":   end,
		"total_lines": len(lines),
	}}
}

// ---- write_file -------------------------------------------------------------

type writeFileTool struct{}

func (writeFileTool) Name() string { return "write_file" }
func (writeFileTool) Description() string {
	return "Create or overwrite a file with the given content. Always provide content; use an empty string only to intentionally create a zero-byte file. Parent directories are created automatically."
}
func (writeFileTool) RequiresApproval() bool { return true }
func (writeFileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":    prop("string", "Destination file path."),
		"content": prop("string", "Complete file content. This field must be present; use an empty string only for an intentional zero-byte file."),
		"append":  propDefault("boolean", "Append instead of overwriting.", false),
	}, "path", "content")
}

func (writeFileTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
		Append  bool    `json:"append"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.Content == nil {
		return Errorf("content is required; provide the complete file content (use an empty string for an intentional empty file)")
	}
	content := *args.Content
	path, err := resolveWrite(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	existed := false
	if info, statErr := os.Stat(path); statErr == nil {
		if info.IsDir() {
			return Errorf("cannot write %s: target is a directory; provide a file path", args.Path)
		}
		existed = true
	} else if !os.IsNotExist(statErr) {
		return Errorf("cannot inspect %s: %v", args.Path, statErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Errorf("cannot create parent directory: %v", err)
	}

	if args.Append {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return Errorf("cannot open %s: %v", args.Path, err)
		}
		defer f.Close()
		if _, err := f.WriteString(content); err != nil {
			return Errorf("cannot append to %s: %v", args.Path, err)
		}
	} else if err := writeWithCheckpoint(in, path, []byte(content), "write_file"); err != nil {
		return Errorf("cannot write %s: %v", args.Path, err)
	}

	verb := "Created"
	if existed {
		verb = "Updated"
	}
	if args.Append {
		verb = "Appended to"
	}
	rel := relTo(in.Workspace, path)
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n") + 1
	}
	return Result{
		Content: fmt.Sprintf("%s %s (%d bytes, %d lines)", verb, rel, len(content), lines),
		Meta:    map[string]any{"path": rel, "bytes": len(content)},
	}
}

// ---- edit_file --------------------------------------------------------------

type editFileTool struct{}

func (editFileTool) Name() string { return "edit_file" }
func (editFileTool) Description() string {
	return "Replace an exact string in a file. The old_string must appear in the file exactly, and exactly once unless replace_all is set. Copy it straight out of read_file output, preserving tabs and spaces; only LF-for-CRLF line endings are reconciled for you."
}
func (editFileTool) RequiresApproval() bool { return true }
func (editFileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":        prop("string", "File to edit."),
		"old_string":  prop("string", "Exact text to find, including indentation (tabs/spaces)."),
		"new_string":  prop("string", "Replacement text."),
		"replace_all": propDefault("boolean", "Replace every occurrence.", false),
	}, "path", "old_string", "new_string")
}

func (editFileTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.OldString == args.NewString {
		return Errorf("old_string and new_string are identical")
	}
	path, err := resolveWrite(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	content := string(data)
	oldString, newString, count, how := resolveEditMatch(content, args.OldString, args.NewString)
	switch {
	case count == 0:
		return Errorf("%s", editNotFoundMessage(args.Path, content, args.OldString))
	case count > 1 && !args.ReplaceAll:
		msg := editAmbiguousMessage(args.Path, content, oldString, count)
		if how != "" {
			// The count and the line numbers describe the translated anchor, so
			// the caller has to be told which string they belong to.
			msg += " [" + how + "]"
		}
		return Errorf("%s", msg)
	}

	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, oldString, newString)
	} else {
		updated = strings.Replace(content, oldString, newString, 1)
	}
	if err := writeWithCheckpoint(in, path, []byte(updated), "edit_file"); err != nil {
		return Errorf("cannot write %s: %v", args.Path, err)
	}
	replaced := count
	if !args.ReplaceAll {
		replaced = 1
	}
	rel := relTo(in.Workspace, path)
	msg := fmt.Sprintf("Edited %s (%d replacement(s))", rel, replaced)
	if how != "" {
		msg += " [" + how + "]"
	}
	// A verbatim match authorises no rewriting of the replacement, so LF breaks
	// in new_string stay LF inside a CRLF file. Every byte asked for is on disk
	// and that is the trade we want, but the mixed endings are invisible until
	// something else surfaces them, so the caller is told. This changes nothing
	// about what was written. newString is the string that actually reached
	// disk, so the LF-to-CRLF recovery — which already translated it — cannot
	// trip this.
	if fileEOL(content) == "\r\n" && strings.Contains(newString, "\n") && !strings.Contains(newString, "\r\n") {
		msg += " Note: the file uses CRLF line endings and new_string used LF, so the replaced region now has LF line breaks. It was written exactly as given; send new_string with \\r\\n breaks if the file must stay consistent."
	}
	return Result{
		Content: msg,
		Meta:    map[string]any{"path": rel, "replacements": replaced},
	}
}

// fileEOL returns the dominant newline sequence used in s, by majority. A
// single stray CRLF or CR in an otherwise-LF file must not decide the flavor:
// that used to convert every multi-line old_string away from what the file
// actually contains and permanently break edits on such files.
func fileEOL(s string) string {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n") - crlf
	cr := strings.Count(s, "\r") - crlf
	if crlf > 0 && crlf >= lf && crlf >= cr {
		return "\r\n"
	}
	if cr > lf {
		return "\r"
	}
	return "\n"
}

// eolOf reports the single newline flavor used in s, or "" when s has no
// newlines or mixes flavors.
func eolOf(s string) string {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n") - crlf
	cr := strings.Count(s, "\r") - crlf
	switch {
	case crlf > 0 && lf == 0 && cr == 0:
		return "\r\n"
	case lf > 0 && crlf == 0 && cr == 0:
		return "\n"
	case cr > 0 && crlf == 0 && lf == 0:
		return "\r"
	}
	return ""
}

// toEOL rewrites every newline in s to the given eol sequence.
func toEOL(s, eol string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if eol == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", eol)
}

// lfToCRLF expands the LF line breaks in s to CRLF and leaves every other byte
// as it was. toEOL cannot stand in for it on a string bound for the file: toEOL
// folds a lone CR into a line break before expanding, and a CR the caller did
// not write as a line break is data. Folding existing CRLF first is what keeps
// the expansion from writing \r\r\n.
func lfToCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// resolveEditMatch locates the bytes old_string names and decides the bytes to
// write in their place. old_string is matched verbatim; the sole recovery is a
// line-ending translation. how names that translation for the result message
// and is empty on a verbatim match, so a caller is never told "exact" when it
// was not.
func resolveEditMatch(content, oldIn, newIn string) (oldString, newString string, count int, how string) {
	if c := strings.Count(content, oldIn); c > 0 {
		return oldIn, newIn, c, ""
	}

	// A model emits \n for a line break whatever the file it read used, so an
	// all-LF anchor against a CRLF file is a well-defined ambiguity rather than
	// a guess, and translating it is lossless. It is reached only once the
	// verbatim match has failed, and new_string is translated only because
	// old_string had to be: a replacement is never rewritten on a path that
	// matched exactly. eolOf has already established that old_string holds no
	// CR at all, so every \n in it is unambiguously a line break.
	if fileEOL(content) == "\r\n" && eolOf(oldIn) == "\n" {
		crlf := lfToCRLF(oldIn)
		if c := strings.Count(content, crlf); c > 0 {
			return crlf, lfToCRLF(newIn), c,
				"translated old_string line endings from LF to CRLF to match the file"
		}
	}

	return oldIn, newIn, 0, ""
}

// lineSpan is the [start,end) byte range of one line's text in the original
// content, excluding its \n, \r\n, or lone \r terminator.
type lineSpan struct{ start, end int }

// lineSpans is the one place the file tools decide where a line begins and
// ends. read_file's numbering and total, grep's match lines and edit_file's
// occurrence lines all come from it, so a line number one tool reports means
// the same line in the next. A line the file terminates is complete: the
// terminator adds no empty line after it, so "a\nb\n" is two lines. Line
// numbers are the 1-based index into the returned slice.
func lineSpans(content string) []lineSpan {
	// A lone CR terminates a line only in a genuinely CR-based file. Anywhere
	// else it is data, and treating it as a break here would number lines
	// differently from what read_file displayed to the model.
	crIsTerminator := fileEOL(content) == "\r"
	var spans []lineSpan
	start := 0
	i := 0
	for i < len(content) {
		switch content[i] {
		case '\n':
			spans = append(spans, lineSpan{start, i})
			i++
			start = i
		case '\r':
			if i+1 < len(content) && content[i+1] == '\n' {
				spans = append(spans, lineSpan{start, i})
				i += 2
				start = i
				continue
			}
			if !crIsTerminator {
				i++
				continue
			}
			spans = append(spans, lineSpan{start, i})
			i++
			start = i
		default:
			i++
		}
	}
	if start < len(content) {
		spans = append(spans, lineSpan{start, len(content)})
	}
	return spans
}

// editNotFoundMessage explains why an edit missed, and names a next step that
// can actually succeed. Whitespace is the usual culprit and the hardest thing
// to see in a diff of two quoted strings, so it is diagnosed first.
func editNotFoundMessage(path, content, oldString string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "old_string not found in %s.", path)

	if strings.Contains(content, "\t") && strings.Contains(oldString, " ") && !strings.Contains(oldString, "\t") {
		// Spaces in old_string might still be inter-word; only flag when a
		// detabbed view of the file contains the old_string.
		for _, width := range []int{2, 4, 8} {
			detabbed := expandTabs(content, width)
			if strings.Contains(detabbed, toEOL(oldString, "\n")) || strings.Contains(detabbed, oldString) {
				fmt.Fprintf(&b, " The file indents with TAB characters, but old_string uses spaces (tab width ~%d). Re-read the file and copy the indentation exactly as it comes back, without expanding tabs.", width)
				return b.String()
			}
		}
	}

	if hint := nearMissHint(content, oldString); hint != "" {
		b.WriteByte(' ')
		b.WriteString(hint)
		return b.String()
	}

	b.WriteString(" Read the file and copy old_string straight out of what it returns; preserve tabs, spaces, and indentation exactly.")
	return b.String()
}

func editAmbiguousMessage(path, content, oldString string, count int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "old_string appears %d times in %s; add unique surrounding context or set replace_all only if every occurrence should change.", count, path)
	if lines := occurrenceLines(content, oldString, 12); len(lines) > 0 {
		b.WriteString(" Current match line(s): ")
		for i, line := range lines {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%d", line)
		}
		b.WriteByte('.')
	}
	b.WriteString(" Re-read the current file and include enough neighbouring lines for exactly one match.")
	return b.String()
}

func occurrenceLines(content, needle string, max int) []int {
	if needle == "" || max <= 0 {
		return nil
	}
	spans := lineSpans(content)
	var lines []int
	for from := 0; from < len(content) && len(lines) < max; {
		i := strings.Index(content[from:], needle)
		if i < 0 {
			break
		}
		at := from + i
		lines = append(lines, lineOfOffset(spans, at))
		from = at + len(needle)
	}
	return lines
}

// lineOfOffset returns the 1-based number of the line containing byte offset
// at. An offset inside a terminator belongs to the line that terminator ends.
func lineOfOffset(spans []lineSpan, at int) int {
	if len(spans) == 0 {
		return 1
	}
	next := sort.Search(len(spans), func(i int) bool { return spans[i].start > at })
	if next == 0 {
		return 1
	}
	return next
}

// nearMissHint reports a few real lines sharing a distinctive identifier with
// old_string. It is intentionally short and bounded: the tool should correct
// the model's stale context without dumping the file into an error response.
func nearMissHint(content, oldString string) string {
	spans := lineSpans(content)
	for _, token := range identifierTokens(oldString) {
		if len(token) < 8 || strings.Contains(strings.ToLower(token), "read_file") {
			continue
		}
		var hits []string
		for i, sp := range spans {
			line := content[sp.start:sp.end]
			if strings.Contains(line, token) {
				line = strings.TrimRight(line, "\r")
				if len(line) > 180 {
					line = line[:180] + "..."
				}
				hits = append(hits, fmt.Sprintf("line %d: %s", i+1, line))
				if len(hits) == 3 {
					break
				}
			}
		}
		if len(hits) > 0 {
			return "Near-miss lines sharing a token (re-read them; do not invent identifiers): " + strings.Join(hits, " ")
		}
	}
	return ""
}

func identifierTokens(s string) []string {
	var out []string
	start := -1
	flush := func(end int) {
		if start >= 0 && end-start >= 8 {
			out = append(out, s[start:end])
		}
		start = -1
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isID := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if isID {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(s))
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if len(out[j]) > len(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// expandTabs replaces leading and embedded tabs with spaces at the given width
// (stop-based), used only for mismatch diagnosis.
func expandTabs(s string, width int) string {
	if width <= 0 {
		width = 4
	}
	var b strings.Builder
	b.Grow(len(s))
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			spaces := width - (col % width)
			b.WriteString(strings.Repeat(" ", spaces))
			col += spaces
		case '\n':
			b.WriteByte('\n')
			col = 0
		case '\r':
			// Keep CR out of the comparison view; pair with LF handling above.
			continue
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

// ---- list_files -------------------------------------------------------------

type listFilesTool struct{}

func (listFilesTool) Name() string { return "list_files" }
func (listFilesTool) Description() string {
	return "List directory entries. Set recursive to walk subdirectories (depth limited)."
}
func (listFilesTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":      propDefault("string", "Directory to list.", "."),
		"recursive": propDefault("boolean", "Walk subdirectories.", false),
		"depth":     propDefault("integer", "Maximum recursion depth.", 3),
		"all":       propDefault("boolean", "Include dotfiles.", false),
	})
}

var ignoredDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".venv": true,
	"venv": true, "dist": true, "build": true, ".next": true, "target": true,
	".cache": true, ".idea": true, ".DS_Store": true,
}

func (listFilesTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Depth     int    `json:"depth"`
		All       bool   `json:"all"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Depth <= 0 {
		args.Depth = 3
	}
	root, err := resolveRead(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}

	var out []string
	count := 0
	var walk func(dir string, depth int, prefix string) error
	walk = func(dir string, depth int, prefix string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return entries[i].Name() < entries[j].Name()
		})
		for _, e := range entries {
			name := e.Name()
			if !args.All && strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() && ignoredDirs[name] {
				out = append(out, prefix+name+"/ (skipped)")
				continue
			}
			if count >= 2000 {
				return errTooMany
			}
			count++
			if e.IsDir() {
				out = append(out, prefix+name+"/")
				if args.Recursive && depth > 1 {
					if err := walk(filepath.Join(dir, name), depth-1, prefix+"  "); err != nil {
						return err
					}
				}
				continue
			}
			size := int64(0)
			if fi, err := e.Info(); err == nil {
				size = fi.Size()
			}
			out = append(out, fmt.Sprintf("%s%s (%s)", prefix, name, humanBytes(size)))
		}
		return nil
	}

	err = walk(root, args.Depth, "")
	if err != nil && !errors.Is(err, errTooMany) {
		return Errorf("cannot list %s: %v", args.Path, err)
	}
	if len(out) == 0 {
		return Text(fmt.Sprintf("%s is empty", relTo(in.Workspace, root)))
	}
	header := fmt.Sprintf("%s (%d entries)\n", relTo(in.Workspace, root), len(out))
	if errors.Is(err, errTooMany) {
		header += "… truncated at 2000 entries\n"
	}
	return Text(header + strings.Join(out, "\n"))
}

var errTooMany = errors.New("too many entries")

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// writeWithCheckpoint keeps a copy of what is there before overwriting it, so
// the change can be undone. A missing checkpoint store is not an error — it
// only means there is nothing to roll back to.
func writeWithCheckpoint(in Input, path string, content []byte, tool string) error {
	if in.Deps != nil && in.Deps.Checkpoint != nil {
		in.Deps.Checkpoint(in.SessionID, path, tool)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	// Record what we just wrote, so an edit-message rollback can distinguish our
	// own output from a later manual edit of the same file.
	if in.Deps != nil && in.Deps.RecordResult != nil {
		sum := sha256.Sum256(content)
		in.Deps.RecordResult(in.SessionID, path, hex.EncodeToString(sum[:]))
	}
	return nil
}
