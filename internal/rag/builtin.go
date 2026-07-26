package rag

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// builtinProvider embeds chunks with the configured model and stores the
// vectors in the Antares database. No external service required.
type builtinProvider struct {
	cfg   *config.Config
	db    store.Store
	embed llm.Client
}

func (p *builtinProvider) Name() string { return "builtin" }

func (p *builtinProvider) Probe(ctx context.Context) (bool, string) {
	model := p.cfg.RAG.EmbedModel
	if model == "" {
		return false, "rag.embed_model is not set"
	}
	if _, err := p.embed.Embed(ctx, model, []string{"ping"}); err != nil {
		return false, fmt.Sprintf("embedding failed: %v", err)
	}
	return true, fmt.Sprintf("built-in vector store · model %s", model)
}

func (p *builtinProvider) Search(ctx context.Context, collection, query string, topK int) ([]tools.RAGResult, error) {
	vecs, err := p.embed.Embed(ctx, p.cfg.RAG.EmbedModel, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedding provider returned no vector")
	}
	chunks, scores, err := p.db.SearchChunks(ctx, collection, vecs[0], query, topK, p.cfg.RAG.Hybrid)
	if err != nil {
		return nil, err
	}
	out := make([]tools.RAGResult, 0, len(chunks))
	for i, c := range chunks {
		out = append(out, tools.RAGResult{
			Content: c.Content, Path: c.Path, DocID: c.DocID, Score: scores[i],
		})
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
		vecs, err := p.embed.Embed(ctx, p.cfg.RAG.EmbedModel, texts)
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
