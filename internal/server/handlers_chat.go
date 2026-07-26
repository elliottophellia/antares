package server

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

var errNotFound = errors.New("not found")

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	err := s.db.Ping(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      err == nil,
		"version": version.Version,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()

	// Provider readiness is a config check, not a live call: the status pill
	// polls every ten seconds and must not bill the user for pings.
	_, provider := cfg.ResolveProvider(cfg.Model.Provider)
	ready := cfg.Model.Default != "" && (provider.APIKey != "" || isLocalEndpoint(provider.BaseURL))

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"version":         version.Version,
		"model":           cfg.Model.Default,
		"provider":        cfg.Model.Provider,
		"provider_ready":  ready,
		"database":        s.db.Driver(),
		"uptime_seconds":  int(time.Since(s.started).Seconds()),
		"active_sessions": s.agent.ActiveCount(),
		"needs_setup":     NeedsSetup(cfg),
	})
}

func isLocalEndpoint(url string) bool {
	l := strings.ToLower(url)
	return strings.Contains(l, "localhost") || strings.Contains(l, "127.0.0.1") || strings.Contains(l, "0.0.0.0")
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	st, err := s.db.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ragProvider := ""
	if p := s.agent.RAG(); p != nil {
		ragProvider = p.Name()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":        version.Version,
		"go_version":     runtime.Version(),
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"uptime_seconds": int(time.Since(s.started).Seconds()),
		"goroutines":     runtime.NumGoroutine(),
		"memory_alloc":   mem.Alloc,
		"memory_sys":     mem.Sys,
		"cpu_count":      runtime.NumCPU(),
		"workspace":      cfg.Agent.Workspace,
		"config_path":    configPath(),
		"database":       map[string]any{"driver": s.db.Driver(), "size_bytes": st.SizeBytes},
		"store":          st,
		"active_shells":  s.agent.Shell().Active(),
		"model":          cfg.Model.Default,
		"provider":       cfg.Model.Provider,
		"rag_provider":   ragProvider,
	})
}

type chatRequest struct {
	SessionID string   `json:"session_id"`
	Message   string   `json:"message"`
	Images    []string `json:"images"` // data URLs or bare base64
	Model     string   `json:"model"`
	Toolset   string   `json:"toolset"`
	UserID    string   `json:"user_id"`
}

// handleChat streams a full agent turn as server-sent events.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" && len(req.Images) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}

	sse, err := newSSE(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Keep intermediaries from closing an idle connection while the model thinks.
	ctx := r.Context()
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				sse.comment("keepalive")
			}
		}
	}()

	agentReq := agent.Request{
		SessionID: req.SessionID,
		Message:   req.Message,
		Images:    decodeImages(req.Images),
		Platform:  "web",
		UserID:    req.UserID,
		Model:     req.Model,
		Toolset:   req.Toolset,
	}

	// Run streams its own error and done events, so nothing is re-sent here.
	emit := func(e agent.Event) error { return sse.send(e) }
	if _, err := s.agent.Run(ctx, agentReq, emit); err != nil && ctx.Err() == nil {
		slog.Debug("chat turn failed", "error", err)
	}
}

// decodeImages accepts data URLs and bare base64 payloads.
func decodeImages(images []string) []llm.Part {
	out := make([]llm.Part, 0, len(images))
	for _, img := range images {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		if strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://") {
			out = append(out, llm.Part{Type: "image", URL: img})
			continue
		}
		mime := "image/png"
		data := img
		if strings.HasPrefix(img, "data:") {
			meta, payload, ok := strings.Cut(strings.TrimPrefix(img, "data:"), ",")
			if !ok {
				continue
			}
			if m, _, found := strings.Cut(meta, ";"); found && m != "" {
				mime = m
			}
			data = payload
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			continue
		}
		out = append(out, llm.Part{Type: "image", MimeType: mime, Data: data})
	}
	return out
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"interrupted": s.agent.Interrupt(body.SessionID)})
}

// ---- sessions ---------------------------------------------------------------

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	f := store.SessionFilter{
		Platform: r.URL.Query().Get("platform"),
		UserID:   r.URL.Query().Get("user_id"),
		Query:    r.URL.Query().Get("q"),
		Limit:    queryInt(r, "limit", 50),
		Offset:   queryInt(r, "offset", 0),
		OrderBy:  r.URL.Query().Get("order_by"),
	}
	sessions, total, err := s.db.ListSessions(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "total": total})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.db.GetSession(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	messages, err := s.db.ListMessages(r.Context(), id, 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess, "messages": messages})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.db.DeleteSession(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sess, err := s.db.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	sess.Title = strings.TrimSpace(body.Title)
	if err := s.db.UpdateSession(r.Context(), sess); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleSearchSessions(w http.ResponseWriter, r *http.Request) {
	hits, err := s.db.SearchMessages(r.Context(), r.URL.Query().Get("q"), queryInt(r, "limit", 30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
}

func (s *Server) handleEmptyCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.CountEmptySessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Server) handleDeleteEmpty(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.DeleteEmptySessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (s *Server) handlePruneSessions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OlderThanDays int `json:"older_than_days"`
	}
	_ = decodeBody(r, &body)
	if body.OlderThanDays <= 0 {
		body.OlderThanDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -body.OlderThanDays)
	n, err := s.db.PruneSessions(r.Context(), cutoff)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
