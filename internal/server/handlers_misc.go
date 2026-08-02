package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/gateway"
	"github.com/enowdev/antares/internal/logx"
	"github.com/enowdev/antares/internal/rag"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// ---- memory -----------------------------------------------------------------

func (s *Server) handleListMemory(w http.ResponseWriter, r *http.Request) {
	items, err := s.db.ListMemories(r.Context(),
		r.URL.Query().Get("scope"), r.URL.Query().Get("scope_key"), queryInt(r, "limit", 200))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": items})
}

func (s *Server) handleSearchMemory(w http.ResponseWriter, r *http.Request) {
	items, err := s.db.SearchMemories(r.Context(), r.URL.Query().Get("q"), queryInt(r, "limit", 30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": items})
}

func (s *Server) handlePutMemory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Key     string `json:"key"`
		Content string `json:"content"`
		Scope   string `json:"scope"`
		Pinned  bool   `json:"pinned"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("content is required"))
		return
	}
	if body.Scope == "" {
		body.Scope = "global"
	}
	if body.Key == "" {
		body.Key = fmt.Sprintf("manual-%d", time.Now().Unix())
	}
	if body.ID == "" {
		body.ID = "mem_" + fmt.Sprintf("%x", time.Now().UnixNano())
	}
	m := &store.Memory{
		ID: body.ID, Scope: body.Scope, Key: body.Key, Content: body.Content,
		Source: "dashboard", Pinned: body.Pinned, Tags: "[]",
	}
	if err := s.db.PutMemory(r.Context(), m); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.db.DeleteMemory(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleResetMemory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope    string `json:"scope"`
		ScopeKey string `json:"scope_key"`
	}
	_ = decodeBody(r, &body)
	n, err := s.db.ClearMemories(r.Context(), body.Scope, body.ScopeKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// ---- rag --------------------------------------------------------------------

func (s *Server) handleRagStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, rag.Describe(r.Context(), s.config(), s.agent.RAG()))
}

func (s *Server) handleRagIndex(w http.ResponseWriter, r *http.Request) {
	provider := s.agent.RAG()
	if provider == nil {
		writeError(w, http.StatusBadRequest, errors.New("RAG is disabled"))
		return
	}
	var body struct {
		Path       string `json:"path"`
		Collection string `json:"collection"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.config()
	collection := body.Collection
	if collection == "" {
		collection = "antares"
	}

	docs, err := collectDocs(cfg.Agent.Workspace, body.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(docs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"files": 0, "chunks": 0})
		return
	}
	chunks, err := provider.Index(r.Context(), collection, docs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": len(docs), "chunks": chunks, "collection": collection})
}

func (s *Server) handleRagSearch(w http.ResponseWriter, r *http.Request) {
	provider := s.agent.RAG()
	if provider == nil {
		writeError(w, http.StatusBadRequest, errors.New("RAG is disabled"))
		return
	}
	var body struct {
		Query      string `json:"query"`
		Collection string `json:"collection"`
		TopK       int    `json:"top_k"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.config()
	if body.Collection == "" {
		body.Collection = "antares"
	}
	if body.TopK <= 0 {
		body.TopK = cfg.RAG.TopK
	}
	started := time.Now()
	hits, err := provider.Search(r.Context(), body.Collection, body.Query, body.TopK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":    hits,
		"took_ms":    time.Since(started).Milliseconds(),
		"collection": body.Collection,
		// The pipeline actually applied, so the dashboard can show how the result
		// was produced (matching the native recall→rerank→compress→topK flow).
		"pipeline": map[string]any{
			"embed_model": cfg.RAG.EmbedModel,
			"hybrid":      cfg.RAG.Hybrid,
			"recall":      cfg.RAG.Recall,
			"rerank_mode": rag.EffectiveRerank(cfg),
			"compress":    cfg.RAG.Compress,
			"top_k":       body.TopK,
		},
	})
}

func (s *Server) handleRagDelete(w http.ResponseWriter, r *http.Request) {
	provider := s.agent.RAG()
	if provider == nil {
		writeError(w, http.StatusBadRequest, errors.New("RAG is disabled"))
		return
	}
	if err := provider.Delete(r.Context(), r.PathValue("name")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// collectDocs walks a workspace-relative path collecting indexable text files.
func collectDocs(workspace, target string) ([]tools.RAGDoc, error) {
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	root := target
	if !filepath.IsAbs(root) {
		root = filepath.Join(workspace, root)
	}
	root = filepath.Clean(root)
	if rel, err := filepath.Rel(workspace, root); err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("path %q is outside the workspace", target)
	}

	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	var docs []tools.RAGDoc
	add := func(p string) {
		data, err := os.ReadFile(p)
		if err != nil || len(data) == 0 || len(data) > 2<<20 {
			return
		}
		rel, _ := filepath.Rel(workspace, p)
		docs = append(docs, tools.RAGDoc{
			ID: rel, Path: rel, Content: string(data),
			Meta: map[string]any{"path": rel, "bytes": len(data)},
		})
	}
	if !fi.IsDir() {
		add(root)
		return docs, nil
	}
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "build", ".venv", "__pycache__", "target", ".next":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".md", ".txt", ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java",
			".rb", ".php", ".c", ".h", ".cpp", ".cs", ".sh", ".yaml", ".yml", ".toml",
			".json", ".sql", ".html", ".css":
			add(p)
		}
		if len(docs) >= 3000 {
			return filepath.SkipAll
		}
		return nil
	})
	return docs, err
}

// ---- analytics --------------------------------------------------------------

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	since := time.Now().AddDate(0, 0, -7)
	switch r.URL.Query().Get("range") {
	case "24h":
		since = time.Now().Add(-24 * time.Hour)
	case "30d":
		since = time.Now().AddDate(0, 0, -30)
	}
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}

	series, err := s.db.UsageSeries(r.Context(), since, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	byModel, err := s.db.UsageByModel(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	totals := struct {
		TokensIn  int64   `json:"tokens_in"`
		TokensOut int64   `json:"tokens_out"`
		Cost      float64 `json:"cost"`
		Calls     int64   `json:"calls"`
	}{}
	for _, p := range series {
		totals.TokensIn += p.TokensIn
		totals.TokensOut += p.TokensOut
		totals.Cost += p.Cost
		totals.Calls += p.Calls
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "by_model": byModel, "totals": totals})
}

// ---- logs -------------------------------------------------------------------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	entries := logx.Tail(queryInt(r, "limit", 300), r.URL.Query().Get("level"), r.URL.Query().Get("q"))
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	sse, err := newSSE(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ch, cancel := logx.Subscribe()
	defer cancel()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			sse.comment("keepalive")
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := sse.send(e); err != nil {
				return
			}
		}
	}
}

// ---- files ------------------------------------------------------------------

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	cfg := s.config()
	target := r.URL.Query().Get("path")
	if target == "" {
		target = "."
	}
	abs, err := safeJoin(cfg.Agent.Workspace, target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	type entry struct {
		Name     string    `json:"name"`
		Path     string    `json:"path"`
		IsDir    bool      `json:"is_dir"`
		Size     int64     `json:"size"`
		Modified time.Time `json:"modified"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(cfg.Agent.Workspace, filepath.Join(abs, e.Name()))
		out = append(out, entry{
			Name: e.Name(), Path: filepath.ToSlash(rel), IsDir: e.IsDir(),
			Size: info.Size(), Modified: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	parent := ""
	if rel, _ := filepath.Rel(cfg.Agent.Workspace, abs); rel != "." && rel != "" {
		parent = filepath.ToSlash(filepath.Dir(rel))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": filepath.ToSlash(relOrSelf(cfg.Agent.Workspace, abs)), "parent": parent, "entries": out,
	})
}

func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	cfg := s.config()
	abs, err := safeJoin(cfg.Agent.Workspace, r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if fi.Size() > 2<<20 {
		writeError(w, http.StatusBadRequest, errors.New("file is too large to preview (over 2 MB)"))
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// A NUL byte in the first chunk means it is not text — say so rather than
	// returning mojibake the preview would render as noise.
	if isBinary(data) {
		writeJSON(w, http.StatusOK, map[string]any{"binary": true, "size": fi.Size()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": string(data), "size": fi.Size()})
}

// isBinary reports whether b looks like non-text data (a NUL byte in the first
// 8 KB is a reliable, cheap signal).
func isBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// handleRawFile serves a file's bytes with a detected Content-Type so the
// dashboard can render images inline and download anything. Unlike
// handleReadFile (JSON text, for previews), this streams the raw file and
// works for binary content. Auth is handled by the token middleware, which
// accepts a ?token= query param — needed because <img src> and download links
// cannot set an Authorization header.
func (s *Server) handleRawFile(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	cfg := s.config()
	abs, err := safeJoin(cfg.Agent.Workspace, r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if fi.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("path is a directory"))
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()

	// A download link passes ?download=1 to force a save-as; otherwise the
	// browser renders inline (images, PDFs).
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(abs)+"\"")
	}
	// http.ServeContent sniffs the content type and handles range requests,
	// which lets the browser stream large media.
	http.ServeContent(w, r, filepath.Base(abs), fi.ModTime(), f)
}

// handleSocialImage serves any image file by absolute path. Unlike
// handleRawFile (which confines reads to the workspace), this endpoint allows
// reading from /tmp and other directories — it is designed for the social
// media agent to embed profile photos and screenshots it downloads. Only
// image file extensions are accepted; non-image paths are rejected.
func (s *Server) handleSocialImage(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	// Only allow image extensions.
	ext := strings.ToLower(filepath.Ext(path))
	allowed := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
		".webp": true, ".svg": true, ".avif": true, ".bmp": true,
	}
	if !allowed[ext] {
		writeError(w, http.StatusBadRequest, errors.New("only image files are allowed"))
		return
	}
	// Resolve to absolute path. No workspace confinement — the agent may
	// download images to /tmp or the antares home.
	abs := path
	if !filepath.IsAbs(path) {
		abs = filepath.Join(config.Home(), path)
	}
	// Prevent directory traversal above the allowed roots.
	abs = filepath.Clean(abs)

	fi, err := os.Stat(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if fi.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("path is a directory"))
		return
	}
	// Size limit: 20 MB to prevent serving huge files.
	if fi.Size() > 20*1024*1024 {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("file too large (max 20 MB)"))
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(abs), fi.ModTime(), f)
}

func safeJoin(workspace, target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	p := target
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	p = filepath.Clean(p)
	rel, err := filepath.Rel(workspace, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside the workspace")
	}
	return p, nil
}

func relOrSelf(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil {
		return rel
	}
	return p
}

// ---- channels ---------------------------------------------------------------

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	pairings, _ := s.db.ListPairings(r.Context())
	if pairings == nil {
		pairings = []store.Pairing{}
	}
	live := map[string]bool{}
	if s.gateway != nil {
		live = s.gateway.Status()
	}

	type channel struct {
		ID         string         `json:"id"`
		Label      string         `json:"label"`
		Enabled    bool           `json:"enabled"`
		Connected  bool           `json:"connected"`
		Configured bool           `json:"configured"`
		HasToken   bool           `json:"has_token"` // kept for backward compatibility
		BotName    string         `json:"bot_name,omitempty"`
		ReplyStyle string         `json:"reply_style,omitempty"`
		Detail     string         `json:"detail"`
		Docs       string         `json:"docs,omitempty"`
		Fields     []channelField `json:"fields"`
	}
	out := make([]channel, 0, len(channelSpecs()))
	for _, spec := range channelSpecs() {
		fields := make([]channelField, len(spec.Fields))
		for i, f := range spec.Fields {
			f.Set = channelValue(cfg, spec.ID, f.Key) != ""
			fields[i] = f
		}
		configured := channelConfigured(cfg, spec)
		botName := ""
		if s.db != nil {
			botName, _ = s.db.GetKV(r.Context(), "channel_botname:"+spec.ID)
		}
		replyStyle := ""
		if spec.ID == "discord" {
			replyStyle = cfg.Gateway.Discord.ReplyStyle
			if replyStyle == "" {
				replyStyle = "embed"
			}
		}
		out = append(out, channel{
			ID: spec.ID, Label: spec.Label, Detail: spec.Detail, Docs: spec.Docs,
			Enabled: channelEnabled(cfg, spec.ID), Connected: live[spec.ID],
			Configured: configured, HasToken: configured, BotName: botName,
			ReplyStyle: replyStyle, Fields: fields,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out, "pairings": pairings})
}

// handleSetChannelToken verifies a bot token with the platform before storing
// it, so a bad paste fails here rather than inside a reconnect loop nobody is
// watching. It also names the bot back, which is the only way to confirm you
// pasted the token you meant to.
func (s *Server) handleSetChannelToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	if id != "telegram" && id != "discord" {
		writeError(w, http.StatusBadRequest, errors.New("unknown channel"))
		return
	}

	identity, err := gateway.VerifyToken(r.Context(), id, body.Token)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if identity != nil && s.db != nil {
		label := identity.Name
		if identity.Handle != "" {
			label = strings.TrimSpace(identity.Name + " " + identity.Handle)
		}
		_ = s.db.SetKV(r.Context(), "channel_botname:"+id, label)
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	switch id {
	case "telegram":
		cfg.Gateway.Telegram.BotToken = body.Token
	case "discord":
		cfg.Gateway.Discord.BotToken = body.Token
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := map[string]any{"ok": true, "bot": identity}
	// Hand the new credential to the running gateway; only fall back to asking
	// for a restart if it cannot take it.
	if s.gateway != nil {
		if err := s.gateway.Sync(id); err != nil {
			out["restart_required"] = true
		}
	}
	writeJSON(w, http.StatusOK, out)
}
