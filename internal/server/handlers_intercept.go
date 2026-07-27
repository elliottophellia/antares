package server

import (
	"net/http"
	"strconv"

	"github.com/enowdev/antares/internal/intercept"
	"github.com/enowdev/antares/internal/tools"
)

// The intercept proxy is a process-wide singleton shared with the agent tool.
func (s *Server) proxy() (*intercept.Proxy, error) { return tools.InterceptProxy() }

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
