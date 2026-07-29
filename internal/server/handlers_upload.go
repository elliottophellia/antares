package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enowdev/antares/internal/tools"
)

// uploadMaxBytes caps a single attachment so a huge file cannot exhaust disk or
// memory. 25 MB comfortably covers documents.
const uploadMaxBytes = 25 << 20

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// handleUpload stores an attached file in a temporary uploads directory (never
// the workspace) and returns a path the read_document tool can resolve. Files
// are grouped per session so they can be cleaned together.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Name      string `json:"name"`
		// Data is base64 (optionally a data: URL); mirrors how images are sent.
		Data string `json:"data"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := sanitizeUploadName(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("a file name is required"))
		return
	}

	payload := body.Data
	if i := strings.Index(payload, ","); strings.HasPrefix(payload, "data:") && i >= 0 {
		payload = payload[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("file data must be base64"))
		return
	}
	if len(data) > uploadMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("file is too large (max 25 MB)"))
		return
	}

	// Per-session subdir under the temp uploads root.
	sub := sanitizeUploadName(body.SessionID)
	if sub == "" {
		sub = "misc"
	}
	dir := filepath.Join(tools.UploadsDir(), sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// The path the model uses with read_document — relative to the uploads root.
	rel := filepath.Join(sub, name)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"path": rel,
		"name": name,
		"size": len(data),
	})
}

func sanitizeUploadName(s string) string {
	s = filepath.Base(strings.TrimSpace(s))
	s = unsafeName.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if len(s) > 128 {
		s = s[len(s)-128:]
	}
	return s
}
