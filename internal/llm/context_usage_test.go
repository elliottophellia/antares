package llm

import "testing"

func TestOpenAIUsageCachedTokensArePromptSubset(t *testing.T) {
	u := (&oaUsage{
		PromptTokens: 145878,
		PromptDetails: &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: 145408},
	}).normalise()

	if got, want := u.ContextSize(), 145878; got != want {
		t.Fatalf("ContextSize() = %d, want %d; cached prompt tokens must not be counted twice", got, want)
	}
	if u.CacheReadTokens != 145408 {
		t.Fatalf("CacheReadTokens = %d, want billing breakdown preserved", u.CacheReadTokens)
	}
}

func TestUsageContextSizeFallbackDoesNotAssumeCacheSemantics(t *testing.T) {
	u := Usage{InputTokens: 100, CacheReadTokens: 20, CacheWriteTokens: 5}
	if got, want := u.ContextSize(), 100; got != want {
		t.Fatalf("ContextSize() fallback = %d, want %d without double-counting unknown cache semantics", got, want)
	}
}

func TestUsageContextSizeHonorsProviderNormalisation(t *testing.T) {
	u := Usage{InputTokens: 100, CacheReadTokens: 20, CacheWriteTokens: 5, ContextTokens: 125}
	if got, want := u.ContextSize(), 125; got != want {
		t.Fatalf("ContextSize() = %d, want provider-normalised %d", got, want)
	}
}
