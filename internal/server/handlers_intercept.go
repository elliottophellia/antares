package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/intercept"
	"github.com/enowdev/antares/internal/tools"
)

// The intercept proxy is a process-wide singleton shared with the agent tool.
func (s *Server) proxy() (*intercept.Proxy, error) { return tools.InterceptProxy() }

// interceptCAPath is where the persisted CA PEM lives (shared with the tool).
func interceptCAPath() string { return config.Path("intercept", "ca-cert.pem") }

func (s *Server) handleInterceptStatus(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	running, addr := p.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"running":   running,
		"addr":      addr,
		"exchanges": len(p.Exchanges()),
		"rules":     len(p.Rules()),
	})
}

func (s *Server) handleInterceptStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port int `json:"port"`
	}
	_ = decodeBody(r, &body)
	if body.Port <= 0 {
		body.Port = 8899
	}
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := p.Start("127.0.0.1:" + strconv.Itoa(body.Port)); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	running, addr := p.Status()
	writeJSON(w, http.StatusOK, map[string]any{"running": running, "addr": addr})
}

func (s *Server) handleInterceptStop(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	p.Stop()
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

func (s *Server) handleInterceptExchanges(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exchanges": p.Exchanges()})
}

func (s *Server) handleInterceptClear(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	p.Clear()
	writeJSON(w, http.StatusOK, map[string]bool{"cleared": true})
}

func (s *Server) handleInterceptCA(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=\"antares-intercept-ca.pem\"")
	_, _ = w.Write(p.CACertPEM())
}

func (s *Server) handleInterceptRules(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"rules": p.Rules()})
	case http.MethodPost:
		var rule intercept.Rule
		if err := decodeBody(r, &rule); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, p.AddRule(rule))
	default:
		writeError(w, http.StatusMethodNotAllowed, errNotFound)
	}
}

func (s *Server) handleInterceptRuleDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p.DeleteRule(id)
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ---- interceptors (browsers, terminal, gated) -------------------------------

func (s *Server) handleInterceptInterceptors(w http.ResponseWriter, r *http.Request) {
	reg := tools.InterceptRegistry()
	type row struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		Category  string `json:"category"`
		Available bool   `json:"available"`
		Reason    string `json:"reason,omitempty"`
	}
	list := reg.List()
	out := make([]row, 0, len(list))
	for _, ic := range list {
		ok, reason := ic.Available(r.Context())
		out = append(out, row{ID: ic.ID(), Label: ic.Label(), Category: ic.Category(), Available: ok, Reason: reason})
	}
	sessions := reg.Sessions()
	sess := make([]map[string]any, 0, len(sessions))
	for _, ss := range sessions {
		sess = append(sess, map[string]any{"id": ss.ID(), "interceptor": ss.Interceptor(), "info": ss.Info()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"interceptors": out, "sessions": sess})
}

func (s *Server) handleInterceptActivate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Interceptor string `json:"interceptor"`
		URL         string `json:"url"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	extra := map[string]any{}
	if body.URL != "" {
		extra["url"] = body.URL
	}
	sess, err := tools.InterceptActivate(r.Context(), body.Interceptor, extra)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": sess.ID(), "interceptor": sess.Interceptor(), "info": sess.Info()})
}

func (s *Server) handleInterceptDeactivate(w http.ResponseWriter, r *http.Request) {
	if err := tools.InterceptRegistry().StopSession(r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

func (s *Server) handleInterceptCertInfo(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cert := p.CA().Cert()
	hash := intercept.SubjectHashOld(cert)
	writeJSON(w, http.StatusOK, map[string]any{
		"fingerprint":  intercept.Fingerprint(cert),
		"subject_hash": hash,
		"spki":         p.SPKIFingerprint(),
		"targets":      intercept.InstallLocations(interceptCAPath(), hash),
	})
}

// ---- breakpoints ------------------------------------------------------------

func (s *Server) handleInterceptBreakpoints(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": p.ListPaused()})
}

func (s *Server) handleInterceptBreakpointStream(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sse, err := newSSE(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ch, cancel := p.SubscribeBreakpoints()
	defer cancel()
	send := func() error { return sse.send(map[string]any{"paused": p.ListPaused()}) }
	if err := send(); err != nil {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			sse.comment("keepalive")
		case _, ok := <-ch:
			if !ok {
				return
			}
			if err := send(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleInterceptBreakpointResume(w http.ResponseWriter, r *http.Request) {
	p, err := s.proxy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var edit intercept.BreakpointResume
	_ = decodeBody(r, &edit)
	if r.URL.Query().Get("abort") == "1" {
		p.Abort(id)
	} else {
		p.Resume(id, edit)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
