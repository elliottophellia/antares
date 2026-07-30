package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// The Proxies API is a global proxy store: a list of named HTTP/SOCKS proxies,
// nothing more. It does not decide when a proxy is used — a tool or the agent
// picks an entry by id/label (config.Proxies.Find) when it wants one, e.g. the
// list_proxies tool and osint_email_full's optional `proxy` argument.

// handleListProxies returns the saved proxies (passwords masked) and the active
// so the dashboard can render the store. Proxies is pure storage — there is no
// "active" one; a tool or the agent picks an entry by id when it wants a proxy.
func (s *Server) handleListProxies(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	out := make([]map[string]any, 0, len(cfg.Proxies.Entries))
	for _, e := range cfg.Proxies.Entries {
		out = append(out, proxyView(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// proxyView renders one entry for the API with its password masked; the URL is
// rebuilt from parts so a stored password never leaks even when URL was typed
// whole.
func proxyView(e config.ProxyEntry) map[string]any {
	pw := ""
	if e.Password != "" {
		pw = "••••••••"
	}
	return map[string]any{
		"id":       e.ID,
		"label":    e.Label,
		"scheme":   firstNonEmpty(e.Scheme, "http"),
		"host":     e.Host,
		"port":     e.Port,
		"username": e.Username,
		"password": pw,
		"url":      redactProxyURL(e),
	}
}

// redactProxyURL returns the dial URL with any password replaced by ••••.
func redactProxyURL(e config.ProxyEntry) string {
	full := e.ProxyURL()
	if full == "" {
		return ""
	}
	u, err := url.Parse(full)
	if err != nil {
		return ""
	}
	if u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			u.User = url.UserPassword(u.User.Username(), "••••")
		}
	}
	return u.String()
}

// handleAddProxy creates or updates a proxy entry. An entry with a matching id
// is updated in place (a blank password keeps the stored one); otherwise a new
// entry is appended with a fresh id.
func (s *Server) handleAddProxy(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Scheme   string `json:"scheme"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		URL      string `json:"url"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	entry := config.ProxyEntry{
		ID:       strings.TrimSpace(body.ID),
		Label:    strings.TrimSpace(body.Label),
		Scheme:   strings.ToLower(strings.TrimSpace(body.Scheme)),
		Host:     strings.TrimSpace(body.Host),
		Port:     body.Port,
		Username: strings.TrimSpace(body.Username),
		Password: body.Password,
		URL:      strings.TrimSpace(body.URL),
	}
	// Require enough to dial: either a URL or a host.
	if entry.URL == "" && entry.Host == "" {
		writeError(w, http.StatusBadRequest, errors.New("a proxy needs a URL or at least a host"))
		return
	}
	if entry.Label == "" {
		entry.Label = firstNonEmpty(entry.Host, entry.URL)
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Update in place when the id matches; a placeholder password preserves the
	// stored secret so a round-trip through the masked view never wipes it.
	updated := false
	if entry.ID != "" {
		for i := range cfg.Proxies.Entries {
			if cfg.Proxies.Entries[i].ID == entry.ID {
				if entry.Password == "" || entry.Password == "••••••••" {
					entry.Password = cfg.Proxies.Entries[i].Password
				}
				cfg.Proxies.Entries[i] = entry
				updated = true
				break
			}
		}
	}
	if !updated {
		if entry.ID == "" {
			entry.ID = newProxyID()
		}
		cfg.Proxies.Entries = append(cfg.Proxies.Entries, entry)
	}

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": entry.ID})
}

// handleBatchAddProxy parses a pasted list of proxies (one per line, any of the
// forms config.ParseProxyLine accepts) and appends the valid ones. It reports
// how many were added and which lines failed, so a bad line never silently
// vanishes.
func (s *Server) handleBatchAddProxy(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// De-dupe against what's already stored, by dial URL.
	seen := map[string]bool{}
	for _, e := range cfg.Proxies.Entries {
		seen[e.ProxyURL()] = true
	}

	var added []string
	var failed []map[string]any
	lines := strings.Split(strings.ReplaceAll(body.Text, "\r\n", "\n"), "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" || strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		entry, perr := config.ParseProxyLine(ln)
		if perr != nil {
			failed = append(failed, map[string]any{"line": i + 1, "text": strings.TrimSpace(ln), "error": perr.Error()})
			continue
		}
		if seen[entry.ProxyURL()] {
			continue // skip duplicates silently
		}
		seen[entry.ProxyURL()] = true
		entry.ID = newProxyID()
		cfg.Proxies.Entries = append(cfg.Proxies.Entries, entry)
		added = append(added, entry.ID)
	}

	if len(added) > 0 {
		if err := config.Save(cfg); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if err := s.applyReload(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": len(added), "failed": failed})
}

// handleDeleteProxy removes an entry from the store.
func (s *Server) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	kept := cfg.Proxies.Entries[:0]
	found := false
	for _, e := range cfg.Proxies.Entries {
		if e.ID == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("proxy not found"))
		return
	}
	cfg.Proxies.Entries = kept
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTestProxy dials a saved entry (by id) or an ad-hoc entry from the body
// and reports the egress IP, so the user can confirm a proxy actually works
// before relying on it.
func (s *Server) handleTestProxy(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		ID       string `json:"id"`
		Scheme   string `json:"scheme"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		URL      string `json:"url"`
	}
	_ = decodeBody(r, &body)

	var proxyURL string
	if id := strings.TrimSpace(body.ID); id != "" {
		cfg := s.config()
		for _, e := range cfg.Proxies.Entries {
			if e.ID == id {
				proxyURL = e.ProxyURL()
				break
			}
		}
		if proxyURL == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "proxy not found"})
			return
		}
	} else {
		e := config.ProxyEntry{
			Scheme: body.Scheme, Host: strings.TrimSpace(body.Host), Port: body.Port,
			Username: strings.TrimSpace(body.Username), Password: body.Password,
			URL: strings.TrimSpace(body.URL),
		}
		// A blank/placeholder password on an existing id means "use the stored one".
		if (e.Password == "" || e.Password == "••••••••") && strings.TrimSpace(body.ID) != "" {
			for _, se := range s.config().Proxies.Entries {
				if se.ID == body.ID {
					e.Password = se.Password
				}
			}
		}
		proxyURL = e.ProxyURL()
	}
	if proxyURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "nothing to test — provide a URL or host"})
		return
	}

	ip, err := proxyEgressIP(r.Context(), proxyURL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ip": ip})
}

// proxyEgressIP fetches the public IP as seen through the proxy.
func proxyEgressIP(ctx context.Context, proxyURL string) (string, error) {
	client, err := config.ProxyHTTPClient(proxyURL, 20*time.Second)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org?format=json", nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal(b, &out) == nil && out.IP != "" {
		return out.IP, nil
	}
	return strings.TrimSpace(string(b)), nil
}

func newProxyID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "px_" + hex.EncodeToString(b[:])
}
