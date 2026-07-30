package rag

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// ProjectCollection returns the RAG collection name for a project directory:
// the folder's own name plus a short path hash — e.g. "enowxgrok-2ba64e54". The
// name makes it readable in the dashboard; the hash keeps it unique so two
// folders sharing a name don't collide, and stable across sessions.
func ProjectCollection(projectDir string) string {
	abs, err := filepath.Abs(strings.TrimSpace(projectDir))
	if err != nil {
		abs = projectDir
	}
	abs = filepath.Clean(abs)
	sum := sha1.Sum([]byte(abs))
	name := sanitizeCollectionName(filepath.Base(abs))
	if name == "" {
		name = "project"
	}
	return name + "-" + hex.EncodeToString(sum[:4])
}

// sanitizeCollectionName keeps a folder name to a safe, compact collection slug.
func sanitizeCollectionName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-_")
}
