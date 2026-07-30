package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/version"
)

// embedder turns text into vectors for the store. It abstracts over the two
// paths: a native Voyage client (its own API shape) and any OpenAI-compatible
// endpoint via the shared llm.Client.
type embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQuery embeds a single search query; backends that distinguish query
	// vs document (Voyage) use it, others fall back to Embed.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// newEmbedder builds the embedder for the configured provider. `voyage` hits the
// Voyage API natively (like enowx-rag did); everything else goes through an
// OpenAI-compatible client resolved from the provider config.
func newEmbedder(cfg *config.Config) (embedder, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.RAG.EmbedProvider))
	if provider == "voyage" {
		key := firstNonBlank(cfg.RAG.EmbedAPIKey, voyageKeyFromProviders(cfg))
		if key == "" {
			return nil, fmt.Errorf("voyage embeddings need an API key (rag.embed_api_key)")
		}
		model := cfg.RAG.EmbedModel
		if model == "" || model == "text-embedding-3-small" {
			model = "voyage-4" // the default; the OpenAI default makes no sense for Voyage
		}
		base := firstNonBlank(cfg.RAG.EmbedBaseURL, "https://api.voyageai.com/v1/embeddings")
		return &voyageEmbedder{
			url: base, model: model, apiKey: key,
			client: &http.Client{Timeout: 60 * time.Second},
		}, nil
	}

	client, err := newEmbedLLM(cfg)
	if err != nil {
		return nil, err
	}
	return &llmEmbedder{client: client, model: cfg.RAG.EmbedModel}, nil
}

// newEmbedLLM builds the OpenAI-compatible client used for embedding, resolving
// endpoint/key from rag.embed_* overrides, then the named provider.
func newEmbedLLM(cfg *config.Config) (llm.Client, error) {
	name := cfg.RAG.EmbedProvider
	if name == "" {
		name = cfg.Model.Provider
	}
	_, p := cfg.ResolveProvider(name)
	baseURL := firstNonBlank(cfg.RAG.EmbedBaseURL, p.BaseURL)
	apiKey := firstNonBlank(cfg.RAG.EmbedAPIKey, p.APIKey)
	return llm.New(llm.Options{
		Kind: p.Kind, BaseURL: baseURL, APIKey: apiKey, Headers: p.Headers, ProviderID: name,
	})
}

// voyageKeyFromProviders lets a user who already configured a "voyage" provider
// reuse that key for embeddings without repeating it under rag.
func voyageKeyFromProviders(cfg *config.Config) string {
	if p, ok := cfg.Providers["voyage"]; ok {
		return p.APIKey
	}
	return ""
}

func firstNonBlank(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ---- OpenAI-compatible embedder (wraps llm.Client) ---------------------------

type llmEmbedder struct {
	client llm.Client
	model  string
}

func (e *llmEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.client.Embed(ctx, e.model, texts)
}

func (e *llmEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.client.Embed(ctx, e.model, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedding provider returned no vector")
	}
	return vecs[0], nil
}

// ---- Voyage embedder (native API) --------------------------------------------

const voyageMaxBatch = 128

type voyageEmbedder struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

func (e *voyageEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, "document")
}

func (e *voyageEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.embed(ctx, []string{text}, "query")
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("voyage returned no vector")
	}
	return vecs[0], nil
}

func (e *voyageEmbedder) embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += voyageMaxBatch {
		end := i + voyageMaxBatch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := e.embedBatch(ctx, texts[i:end], inputType)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (e *voyageEmbedder) embedBatch(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{
		"input": texts, "model": e.model, "input_type": inputType,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("voyage returned %d: %s", resp.StatusCode, msg)
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	// Order by index to be safe, then extract.
	vecs := make([][]float32, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	return vecs, nil
}
