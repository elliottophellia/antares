package tools

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/board"
	"github.com/enowdev/antares/internal/store"
)

// ---- memory -----------------------------------------------------------------

type memoryTool struct{}

func (memoryTool) Name() string { return "memory" }
func (memoryTool) Description() string {
	return "Long-term memory. Save durable facts about the user or project, search them, list them, or delete one. " +
		"Save only what stays true across sessions — not transient task state."
}
func (memoryTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":  propEnum("What to do.", "save", "search", "list", "delete"),
		"content": prop("string", "For save: the fact to remember, one self-contained sentence."),
		"key":     prop("string", "For save: short slug identifying this memory; reusing a key updates it."),
		"query":   prop("string", "For search: what to look for."),
		"scope":   propEnum("Which memory bucket to use.", "global", "user", "session", "project"),
		"id":      prop("string", "For delete: the memory id."),
		"limit":   propDefault("integer", "Maximum results.", 20),
	}, "action")
}

func (memoryTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Action  string `json:"action"`
		Content string `json:"content"`
		Key     string `json:"key"`
		Query   string `json:"query"`
		Scope   string `json:"scope"`
		ID      string `json:"id"`
		Limit   int    `json:"limit"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Store == nil {
		return Errorf("memory storage is not available")
	}
	db := in.Deps.Store
	if args.Scope == "" {
		args.Scope = "global"
	}
	scopeKey := ""
	switch args.Scope {
	case "session":
		scopeKey = in.SessionID
	case "user":
		scopeKey = in.UserID
	}
	if args.Limit <= 0 {
		args.Limit = 20
	}

	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "save", "store", "add":
		content := strings.TrimSpace(args.Content)
		if content == "" {
			return Errorf("content is required when saving a memory")
		}
		key := strings.TrimSpace(args.Key)
		if key == "" {
			key = slugify(content, 48)
		}
		id := memoryID(args.Scope, scopeKey, key)
		m := &store.Memory{
			ID: id, Scope: args.Scope, ScopeKey: scopeKey, Key: key,
			Content: content, Source: "agent", Tags: "[]",
		}
		if err := db.PutMemory(ctx, m); err != nil {
			return Errorf("save failed: %v", err)
		}
		return Text(fmt.Sprintf("Saved memory %q in scope %s.", key, args.Scope))

	case "search", "recall", "find":
		q := strings.TrimSpace(args.Query)
		if q == "" {
			return Errorf("query is required when searching memory")
		}
		found, err := db.SearchMemories(ctx, q, args.Limit)
		if err != nil {
			return Errorf("search failed: %v", err)
		}
		if len(found) == 0 {
			return Text(fmt.Sprintf("No memories match %q.", q))
		}
		return Text(renderMemories(found))

	case "list":
		found, err := db.ListMemories(ctx, args.Scope, scopeKey, args.Limit)
		if err != nil {
			return Errorf("list failed: %v", err)
		}
		if len(found) == 0 {
			return Text("No memories stored yet.")
		}
		return Text(renderMemories(found))

	case "delete", "forget", "remove":
		if strings.TrimSpace(args.ID) == "" {
			return Errorf("id is required when deleting a memory")
		}
		if err := db.DeleteMemory(ctx, args.ID); err != nil {
			return Errorf("delete failed: %v", err)
		}
		return Text("Memory deleted.")

	default:
		return Errorf("unknown action %q (want save, search, list, or delete)", args.Action)
	}
}

func renderMemories(items []store.Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d memory item(s):\n\n", len(items))
	for _, m := range items {
		fmt.Fprintf(&b, "- [%s] %s\n  %s\n  (id: %s, updated %s)\n",
			m.Scope, m.Key, m.Content, m.ID, m.UpdatedAt.Format(time.RFC3339))
	}
	return b.String()
}

func memoryID(scope, scopeKey, key string) string {
	sum := sha1.Sum([]byte(scope + "|" + scopeKey + "|" + key))
	return "mem_" + hex.EncodeToString(sum[:10])
}

func slugify(s string, maxLen int) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
		if b.Len() >= maxLen {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// ---- session_search ---------------------------------------------------------

type sessionSearchTool struct{}

func (sessionSearchTool) Name() string { return "session_search" }
func (sessionSearchTool) Description() string {
	return "Full-text search across past conversations. Use it to recall what was discussed or decided in earlier sessions."
}
func (sessionSearchTool) Schema() map[string]any {
	return schema(map[string]any{
		"query": prop("string", "Words to search for across all past messages."),
		"limit": propDefault("integer", "Maximum matches.", 15),
	}, "query")
}

func (sessionSearchTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Store == nil {
		return Errorf("session storage is not available")
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 15
	}
	hits, err := in.Deps.Store.SearchMessages(ctx, q, args.Limit)
	if err != nil {
		return Errorf("search failed: %v", err)
	}
	if len(hits) == 0 {
		return Text(fmt.Sprintf("No past messages match %q.", q))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d match(es) for %q:\n\n", len(hits), q)
	for _, h := range hits {
		title := h.SessionTitle
		if title == "" {
			title = h.SessionID
		}
		fmt.Fprintf(&b, "- [%s] %s (%s)\n  %s\n", h.Role, title, h.CreatedAt.Format("2006-01-02 15:04"), h.Snippet)
	}
	return Text(b.String())
}

// ---- rag_search / rag_index -------------------------------------------------

type ragSearchTool struct{}

func (ragSearchTool) Name() string { return "rag_search" }
func (ragSearchTool) Description() string {
	return "Semantic search over indexed documents and codebases. Use it before reading files when you need to locate relevant context."
}
func (ragSearchTool) Schema() map[string]any {
	return schema(map[string]any{
		"query":      prop("string", "Natural-language question or description of what to find."),
		"collection": prop("string", "Collection/project to search. Defaults to the configured project."),
		"top_k":      propDefault("integer", "Number of passages to return.", 8),
	}, "query")
}

func (ragSearchTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Query      string `json:"query"`
		Collection string `json:"collection"`
		TopK       int    `json:"top_k"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.RAG == nil {
		return Errorf("RAG is disabled. Enable it with rag.enabled = true and pick rag.provider (builtin or enowx).")
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return Errorf("query is required")
	}
	collection := firstNonBlank(args.Collection, defaultCollection(in))
	topK := args.TopK
	if topK <= 0 {
		topK = in.Deps.Config.RAG.TopK
	}
	if topK <= 0 {
		topK = 8
	}

	hits, err := in.Deps.RAG.Search(ctx, collection, q, topK)
	if err != nil {
		return Errorf("rag search failed: %v", err)
	}
	if len(hits) == 0 {
		return Text(fmt.Sprintf("No indexed passages match %q in collection %q.", q, collection))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d passage(s) from %s (%s):\n\n", len(hits), collection, in.Deps.RAG.Name())
	for i, h := range hits {
		label := h.Path
		if label == "" {
			label = h.DocID
		}
		fmt.Fprintf(&b, "--- [%d] %s (score %.3f)\n%s\n\n", i+1, label, h.Score, truncateText(h.Content, 3000))
	}
	return Text(b.String())
}

type ragIndexTool struct{}

func (ragIndexTool) Name() string { return "rag_index" }
func (ragIndexTool) Description() string {
	return "Index a file or directory into the semantic search index so it can be retrieved later with rag_search."
}
func (ragIndexTool) RequiresApproval() bool { return true }
func (ragIndexTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":       prop("string", "File or directory to index (relative to the workspace)."),
		"collection": prop("string", "Collection to index into. Defaults to the configured project."),
		"include":    prop("string", "Optional glob filter, e.g. **/*.go"),
	}, "path")
}

// indexableExt limits directory walks to text formats worth embedding.
var indexableExt = map[string]bool{
	".md": true, ".txt": true, ".go": true, ".py": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".rs": true, ".java": true, ".rb": true, ".php": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cs": true, ".sh": true,
	".yaml": true, ".yml": true, ".toml": true, ".json": true, ".sql": true, ".html": true, ".css": true,
}

func (ragIndexTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Path       string `json:"path"`
		Collection string `json:"collection"`
		Include    string `json:"include"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.RAG == nil {
		return Errorf("RAG is disabled. Enable it with rag.enabled = true.")
	}
	root, err := resolvePath(in.Workspace, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	collection := firstNonBlank(args.Collection, defaultCollection(in))

	var includeRe = func(string) bool { return true }
	if args.Include != "" {
		re, err := globToRegexp(args.Include)
		if err != nil {
			return Errorf("invalid include pattern: %v", err)
		}
		includeRe = re.MatchString
	}

	var docs []RAGDoc
	fi, err := os.Stat(root)
	if err != nil {
		return Errorf("cannot index %s: %v", args.Path, err)
	}
	add := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 || len(data) > 2<<20 {
			return
		}
		rel := relTo(in.Workspace, path)
		docs = append(docs, RAGDoc{
			ID: hashID(rel), Path: rel, Content: string(data),
			Meta: map[string]any{"path": rel, "bytes": len(data)},
		})
	}
	if fi.IsDir() {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if ignoredDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !indexableExt[strings.ToLower(filepath.Ext(p))] {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			if !includeRe(filepath.ToSlash(rel)) {
				return nil
			}
			add(p)
			if len(docs) >= 3000 {
				return filepath.SkipAll
			}
			return nil
		})
	} else {
		add(root)
	}

	if len(docs) == 0 {
		return Text("Nothing to index (no matching text files found).")
	}
	in.Emit(Progress{Tool: "rag_index", Message: fmt.Sprintf("indexing %d file(s)…", len(docs))})
	n, err := in.Deps.RAG.Index(ctx, collection, docs)
	if err != nil {
		return Errorf("indexing failed: %v", err)
	}
	return Text(fmt.Sprintf("Indexed %d file(s) into collection %q as %d chunk(s) via %s.",
		len(docs), collection, n, in.Deps.RAG.Name()))
}

func defaultCollection(in Input) string {
	if in.Deps != nil && in.Deps.Config != nil {
		if p := strings.TrimSpace(in.Deps.Config.RAG.EnowxProject); p != "" {
			return p
		}
	}
	return "antares"
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func hashID(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:12])
}

// ---- todo -------------------------------------------------------------------

// TodoItem is one entry in the session task list.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending|in_progress|completed
}

type todoTool struct{}

func (todoTool) Name() string { return "todo" }
func (todoTool) Description() string {
	return "Track a task list for the current session. On every write you MUST send the ENTIRE list — " +
		"the tool replaces it wholesale, so keep already-completed items marked \"completed\" and never drop items. " +
		"Mark each finished item \"completed\" as you go. Use it for multi-step work so progress stays visible."
}
func (todoTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propEnum("read returns the list; write replaces it.", "read", "write"),
		"items": map[string]any{
			"type":        "array",
			"description": "Complete task list (required for write).",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": prop("string", "What needs doing."),
					"status":  propEnum("Task state.", "pending", "in_progress", "completed"),
				},
				"required": []string{"content", "status"},
			},
		},
	}, "action")
}

func (todoTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Action string     `json:"action"`
		Items  []TodoItem `json:"items"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Store == nil {
		return Errorf("todo storage is not available")
	}
	key := "todo:" + in.SessionID

	if strings.EqualFold(args.Action, "write") {
		blob, err := json.Marshal(args.Items)
		if err != nil {
			return Errorf("%v", err)
		}
		if err := in.Deps.Store.SetKV(ctx, key, string(blob)); err != nil {
			return Errorf("save failed: %v", err)
		}
		// Mirror the list onto the Kanban board so the board page reflects the
		// task list in realtime: pending→todo, in_progress→doing, completed→done.
		if in.Deps.Board != nil {
			mirror := make([]board.TodoMirror, len(args.Items))
			for i, it := range args.Items {
				mirror[i] = board.TodoMirror{Content: it.Content, Status: it.Status}
			}
			_ = in.Deps.Board.SyncTodos(in.SessionID, mirror)
		}
		return Result{Content: renderTodos(args.Items), Meta: map[string]any{"todos": args.Items}}
	}

	raw, err := in.Deps.Store.GetKV(ctx, key)
	if err != nil {
		return Text("Task list is empty.")
	}
	var items []TodoItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return Text("Task list is empty.")
	}
	return Result{Content: renderTodos(items), Meta: map[string]any{"todos": items}}
}

func renderTodos(items []TodoItem) string {
	if len(items) == 0 {
		return "Task list is empty."
	}
	var b strings.Builder
	done := 0
	for _, it := range items {
		mark := "[ ]"
		switch it.Status {
		case "completed":
			mark = "[x]"
			done++
		case "in_progress":
			mark = "[~]"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, it.Content)
	}
	fmt.Fprintf(&b, "\n%d/%d complete", done, len(items))
	return b.String()
}
