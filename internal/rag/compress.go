package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/enowdev/antares/internal/tools"
)

// dedupe drops near-duplicate results, keeping the first (highest-ranked)
// occurrence. Two results collide when their trimmed content hashes match —
// exact-content dedup, the same approach enowx-rag used. It preserves order.
func dedupe(in []tools.RAGResult) []tools.RAGResult {
	seen := make(map[string]bool, len(in))
	out := make([]tools.RAGResult, 0, len(in))
	for _, r := range in {
		h := contentHash(r.Content)
		if h == "" || seen[h] {
			if h == "" {
				out = append(out, r) // empty content is not a dup of anything
			}
			continue
		}
		seen[h] = true
		out = append(out, r)
	}
	return out
}

func contentHash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
