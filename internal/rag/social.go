package rag

import "strings"

// SocialCollection returns the RAG collection name for a social media platform.
// "social/instagram" → "social-instagram", "social/shared" → "social-shared".
// Use this to namespace platform-specific knowledge so the Social Media agent
// can retrieve context scoped to the platform it's working on.
func SocialCollection(platform string) string {
	platform = strings.TrimSpace(strings.ToLower(platform))
	if platform == "" {
		platform = "shared"
	}
	platform = strings.ReplaceAll(platform, "/", "-")
	return "social-" + sanitizeCollectionName(platform)
}
