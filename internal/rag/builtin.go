package rag

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// builtinProvider embeds chunks with the configured model and stores the
// vectors in the Antares database. It recalls candidates by hybrid similarity,
// then reranks and dedups them. No external service required.
type builtinProvider struct {
	cfg    *config.Config
	db     store.Store
	embed  embedder
	rerank reranker // nil = keep retrieval order
}

func (p *builtinProvider) Name() string { return "builtin" }

func (p *builtinProvider) Probe(ctx context.Context) (bool, string) {
	model := p.cfg.RAG.EmbedModel
	if model == "" {
		return false, "rag.embed_model is not set"
	}
	if _, err := p.embed.Embed(ctx, []string{"ping"}); err != nil {
		return false, fmt.Sprintf("embedding failed: %v", err)
	}
	return true, fmt.Sprintf("built-in vector store · model %s", model)
}

func (p *builtinProvider) Search(ctx context.Context, collection, query string, topK int) ([]tools.RAGResult, error) {
	if topK <= 0 {
		topK = p.cfg.RAG.TopK
	}
	// Recall a wider set than we return, so rerank and dedup have room to work.
	recall := p.cfg.RAG.Recall
	if recall < topK {
		recall = topK
	}

	// EmbedQuery lets a backend that distinguishes query vs document input
	// (Voyage) embed the query with the right type; others just embed it.
	qvec, err := p.embed.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	chunks, scores, err := p.db.SearchChunks(ctx, collection, qvec, query, recall, p.cfg.RAG.Hybrid)
	if err != nil {
		return nil, err
	}
	out := make([]tools.RAGResult, 0, len(chunks))
	for i, c := range chunks {
		out = append(out, tools.RAGResult{
			Content: c.Content, Path: c.Path, DocID: c.DocID, Score: scores[i],
		})
	}

	// Rerank the candidates (best-effort — returns retrieval order on any error).
	if p.rerank != nil && len(out) > 1 {
		out = p.rerank.rerank(ctx, query, out, recall)
	}
	// Collapse near-duplicates before narrowing to the final top-K.
	if p.cfg.RAG.Compress {
		out = dedupe(out)
	}
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

// embedBatchSize keeps request bodies small enough for strict gateways.
const embedBatchSize = 64

func (p *builtinProvider) Index(ctx context.Context, collection string, docs []tools.RAGDoc) (int, error) {
	type pending struct {
		chunk store.Chunk
		text  string
	}
	var queue []pending

	for _, d := range docs {
		parts := chunkText(d.Content, p.cfg.RAG.ChunkSize, p.cfg.RAG.ChunkOverlap)
		for i, part := range parts {
			meta := store.Meta{}
			for k, v := range d.Meta {
				meta[k] = v
			}
			queue = append(queue, pending{
				chunk: store.Chunk{
					ID:         chunkID(collection, d.ID, i),
					Collection: collection,
					DocID:      d.ID,
					Path:       d.Path,
					Index:      i,
					Content:    part,
					Meta:       meta,
				},
				text: part,
			})
		}
	}
	if len(queue) == 0 {
		return 0, nil
	}

	written := 0
	for start := 0; start < len(queue); start += embedBatchSize {
		end := min(start+embedBatchSize, len(queue))
		batch := queue[start:end]

		texts := make([]string, len(batch))
		for i, q := range batch {
			texts[i] = q.text
		}
		vecs, err := p.embed.Embed(ctx, texts)
		if err != nil {
			return written, fmt.Errorf("embed batch %d-%d: %w", start, end, err)
		}
		chunks := make([]store.Chunk, 0, len(batch))
		for i, q := range batch {
			if i < len(vecs) && len(vecs[i]) > 0 {
				q.chunk.Embedding = vecs[i]
			}
			chunks = append(chunks, q.chunk)
		}
		if err := p.db.PutChunks(ctx, chunks); err != nil {
			return written, err
		}
		written += len(chunks)
		slog.Debug("rag indexed batch", "collection", collection, "chunks", len(chunks))
	}
	return written, nil
}

func (p *builtinProvider) Collections(ctx context.Context) ([]string, error) {
	return p.db.ListCollections(ctx)
}

func (p *builtinProvider) Delete(ctx context.Context, collection string) error {
	_, err := p.db.DeleteCollection(ctx, collection)
	return err
}

func chunkID(collection, docID string, index int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%d", collection, docID, index)))
	return "chk_" + hex.EncodeToString(sum[:12])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
