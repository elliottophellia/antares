// Package rag provides pluggable retrieval backends: a builtin vector store
// backed by the Antares database, or a remote enowx-rag daemon.
package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// Status summarises the active backend for the dashboard.
type Status struct {
	Enabled     bool     `json:"enabled"`
	Provider    string   `json:"provider"`
	Collections []string `json:"collections"`
	Reachable   bool     `json:"reachable"`
	Detail      string   `json:"detail"`
}

// Prober is implemented by backends that can report reachability.
type Prober interface {
	Probe(ctx context.Context) (bool, string)
}

// New builds the retrieval provider selected in config. It returns nil when RAG
// is disabled, which callers treat as "tool unavailable".
func New(cfg *config.Config, db store.Store) (tools.RAGProvider, error) {
	if !cfg.RAG.Enabled {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(cfg.RAG.Provider)) {
	case "", "builtin", "local":
		embedder, err := newEmbedder(cfg)
		if err != nil {
			return nil, fmt.Errorf("builtin rag: %w", err)
		}
		return &builtinProvider{cfg: cfg, db: db, embed: embedder}, nil
	case "enowx", "enowx-rag":
		return newEnowxProvider(cfg), nil
	case "none", "off":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown rag provider %q (want builtin, enowx, or none)", cfg.RAG.Provider)
	}
}

// Describe reports the current backend status for /api/rag/status.
func Describe(ctx context.Context, cfg *config.Config, p tools.RAGProvider) Status {
	st := Status{Enabled: cfg.RAG.Enabled}
	if p == nil {
		st.Detail = "RAG is disabled in the configuration."
		return st
	}
	st.Provider = p.Name()
	st.Reachable = true
	if pr, ok := p.(Prober); ok {
		st.Reachable, st.Detail = pr.Probe(ctx)
	}
	if cols, err := p.Collections(ctx); err == nil {
		st.Collections = cols
	} else if st.Detail == "" {
		st.Detail = err.Error()
	}
	return st
}

// newEmbedder builds the LLM client used to embed text for the builtin store.
func newEmbedder(cfg *config.Config) (llm.Client, error) {
	name := cfg.RAG.EmbedProvider
	if name == "" {
		name = cfg.Model.Provider
	}
	_, p := cfg.ResolveProvider(name)
	baseURL := cfg.RAG.EmbedBaseURL
	if baseURL == "" {
		baseURL = p.BaseURL
	}
	apiKey := cfg.RAG.EmbedAPIKey
	if apiKey == "" {
		apiKey = p.APIKey
	}
	return llm.New(llm.Options{
		Kind: p.Kind, BaseURL: baseURL, APIKey: apiKey, Headers: p.Headers, ProviderID: name,
	})
}

// chunkText splits content into overlapping windows, preferring paragraph and
// line boundaries so chunks stay semantically whole.
func chunkText(content string, size, overlap int) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if size <= 0 {
		size = 1200
	}
	if overlap < 0 || overlap >= size {
		overlap = size / 6
	}
	runes := []rune(content)
	if len(runes) <= size {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	}

	var out []string
	for start := 0; start < len(runes); {
		end := start + size
		if end >= len(runes) {
			chunk := strings.TrimSpace(string(runes[start:]))
			if chunk != "" {
				out = append(out, chunk)
			}
			break
		}
		// Back off to the nearest paragraph break, then line break, within the
		// last third of the window.
		cut := end
		floor := start + size*2/3
		for i := end; i > floor; i-- {
			if runes[i] == '\n' && i > 0 && runes[i-1] == '\n' {
				cut = i
				break
			}
		}
		if cut == end {
			for i := end; i > floor; i-- {
				if runes[i] == '\n' {
					cut = i
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(runes[start:cut]))
		if chunk != "" {
			out = append(out, chunk)
		}
		next := cut - overlap
		if next <= start {
			next = cut
		}
		start = next
	}
	return out
}
