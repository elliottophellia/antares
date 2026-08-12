package rag

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// UserCollection returns the per-user RAG collection name for a gateway sender,
// namespaced by platform and user id: "discord" + "12345" → "user-discord-<h>".
// The id is hashed so an arbitrary platform id (which may contain characters a
// collection name cannot) always yields a safe, stable name. Returns "" when
// there is no user to scope to, so callers can skip per-user work.
func UserCollection(platform, userID string) string {
	platform = strings.TrimSpace(strings.ToLower(platform))
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	if platform == "" {
		platform = "chat"
	}
	sum := sha1.Sum([]byte(platform + ":" + userID))
	return "user-" + sanitizeCollectionName(platform) + "-" + hex.EncodeToString(sum[:6])
}
