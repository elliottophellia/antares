// Package rag is Antares' native retrieval store: it embeds chunks with the
// configured model, keeps the vectors in the Antares database, and layers
// reranking and near-duplicate compression on top of hybrid search. There is no
// external service — it all runs in-process.
package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// Status summarises the active backend for the dashboard.
type Status struct {
	Enabled     bool        `json:"enabled"`
	Provider    string      `json:"provider"`
	Collections []string    `json:"collections"`
	Reachable   bool        `json:"reachable"`
	Detail      string      `json:"detail"`
	Pipeline    *StatusPipe `json:"pipeline,omitempty"`
}

// StatusPipe describes the configured retrieval pipeline, so the dashboard can
// show how searches run before one is even issued.
type StatusPipe struct {
	EmbedProvider string `json:"embed_provider"`
	EmbedModel    string `json:"embed_model"`
	Hybrid        bool   `json:"hybrid"`
	Recall        int    `json:"recall"`
	RerankMode    string `json:"rerank_mode"`
	Compress      bool   `json:"compress"`
	TopK          int    `json:"top_k"`
	AutoContext   bool   `json:"auto_context"`
}

// Prober is implemented by backends that can report reachability.
type Prober interface {
	Probe(ctx context.Context) (bool, string)
}

// New builds the native retrieval provider. It returns nil when RAG is disabled,
// which callers treat as "tool unavailable".
func New(cfg *config.Config, db store.Store) (tools.RAGProvider, error) {
	if !cfg.RAG.Enabled {
		return nil, nil
	}
	embedder, err := newEmbedder(cfg)
	if err != nil {
		return nil, fmt.Errorf("rag: %w", err)
	}
	return &builtinProvider{cfg: cfg, db: db, embed: embedder, rerank: newReranker(cfg)}, nil
}

// Describe reports the current backend status for /api/rag/status.
func Describe(ctx context.Context, cfg *config.Config, p tools.RAGProvider) Status {
	st := Status{Enabled: cfg.RAG.Enabled}
	if cfg.RAG.Enabled {
		st.Pipeline = &StatusPipe{
			EmbedProvider: cfg.RAG.EmbedProvider, EmbedModel: cfg.RAG.EmbedModel,
			Hybrid: cfg.RAG.Hybrid, Recall: cfg.RAG.Recall, RerankMode: EffectiveRerank(cfg),
			Compress: cfg.RAG.Compress, TopK: cfg.RAG.TopK, AutoContext: cfg.RAG.AutoContext,
		}
	}
	if p == nil {
		// Enabled in config but the provider could not be built (e.g. missing
		// embedding key) — say so rather than just "disabled".
		if cfg.RAG.Enabled {
			st.Detail = "RAG is enabled but the embedding provider is not configured (set rag.embed_api_key)."
		} else {
			st.Detail = "RAG is disabled in the configuration."
		}
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
