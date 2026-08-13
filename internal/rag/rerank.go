package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/textutil"
	"github.com/enowdev/antares/internal/tools"
	"github.com/enowdev/antares/internal/version"
)

// reranker reorders recalled candidates by relevance to the query, returning at
// most topK. Unlike the embedding step (which finds candidates by vector
// similarity), a reranker judges the query against each candidate's text
// directly — the two are complementary.
type reranker interface {
	rerank(ctx context.Context, query string, in []tools.RAGResult, topK int) []tools.RAGResult
}

// newReranker builds the reranker for the configured mode:
//
//   - "off" (or unknown): no rerank — keep retrieval order (fastest).
//   - "api": an explicit external reranker (rerank_url + key).
//   - "llm" (default, "smart auto"): if Voyage embeddings are configured, use the
//     fast Voyage rerank API automatically; otherwise fall back to an auxiliary
//     model scoring the candidates; if neither is available, skip rerank rather
//     than slow things down.
//
// Returning nil means "no rerank".
func newReranker(cfg *config.Config) reranker {
	switch strings.ToLower(strings.TrimSpace(cfg.RAG.RerankMode)) {
	case "off", "none":
		return nil

	case "api":
		if strings.TrimSpace(cfg.RAG.RerankURL) == "" {
			return nil
		}
		return &apiReranker{
			url:    strings.TrimRight(cfg.RAG.RerankURL, "/"),
			model:  firstNonBlank(cfg.RAG.RerankModel, "rerank-2.5"),
			apiKey: cfg.RAG.RerankAPIKey,
			client: &http.Client{Timeout: 30 * time.Second},
		}

	case "llm", "":
		// Prefer Voyage rerank when we have a Voyage key — it is far faster than a
		// full LLM scoring pass and purpose-built for reranking.
		if key := voyageKeyFor(cfg); key != "" {
			return &apiReranker{
				url:    "https://api.voyageai.com/v1/rerank",
				model:  firstNonBlank(cfg.RAG.RerankModel, "rerank-2.5"),
				apiKey: key,
				client: &http.Client{Timeout: 30 * time.Second},
			}
		}
		// No Voyage key → score with the auxiliary model; if even that can't be
		// built, skip rerank rather than fail RAG.
		client, model, err := newRerankLLM(cfg)
		if err != nil {
			return nil
		}
		return &llmReranker{client: client, model: model}

	default:
		return nil
	}
}

// EffectiveRerank reports which reranker actually runs for the current config,
// so the dashboard shows the truth (mode "llm" may resolve to Voyage). Returns
// one of: "off", "voyage", "api", "llm".
func EffectiveRerank(cfg *config.Config) string {
	switch strings.ToLower(strings.TrimSpace(cfg.RAG.RerankMode)) {
	case "off", "none":
		return "off"
	case "api":
		if strings.TrimSpace(cfg.RAG.RerankURL) == "" {
			return "off"
		}
		return "api"
	default: // llm / empty = smart auto
		if voyageKeyFor(cfg) != "" {
			return "voyage"
		}
		if _, _, err := newRerankLLM(cfg); err == nil {
			return "llm"
		}
		return "off"
	}
}

// voyageKeyFor returns a Voyage API key for reranking: an explicit rerank key, a
// Voyage embedding key (when embed_provider is voyage), or a configured "voyage"
// provider key. Empty when Voyage isn't available.
func voyageKeyFor(cfg *config.Config) string {
	if k := strings.TrimSpace(cfg.RAG.RerankAPIKey); k != "" {
		return k
	}
	if strings.EqualFold(strings.TrimSpace(cfg.RAG.EmbedProvider), "voyage") {
		if k := strings.TrimSpace(cfg.RAG.EmbedAPIKey); k != "" {
			return k
		}
	}
	return strings.TrimSpace(voyageKeyFromProviders(cfg))
}

// newRerankLLM builds the chat client used for LLM reranking, preferring the
// configured auxiliary model and falling back to the default model.
func newRerankLLM(cfg *config.Config) (llm.Client, string, error) {
	providerID := cfg.Model.Provider
	model := strings.TrimSpace(cfg.Model.Auxiliary)
	if model == "" {
		model = cfg.Model.Default
	}
	_, p := cfg.ResolveProvider(providerID)
	client, err := llm.New(llm.Options{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey, Headers: p.Headers, ProviderID: providerID,
	})
	if err != nil {
		return nil, "", err
	}
	return client, model, nil
}

// ---- LLM reranker ------------------------------------------------------------

type llmReranker struct {
	client llm.Client
	model  string
}

const rerankSystem = `You rank passages by how well they answer a query. You are given a query and a numbered list of passages. Reply with ONLY a JSON array of objects {"i": <passage number>, "s": <relevance 0-10>}, most relevant first, for the passages that are actually relevant. Do not include unrelated passages. No prose, no code fences.`

func (r *llmReranker) rerank(ctx context.Context, query string, in []tools.RAGResult, topK int) []tools.RAGResult {
	if len(in) <= 1 {
		return clamp(in, topK)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Query: %s\n\nPassages:\n", query)
	for i, c := range in {
		body := textutil.TruncateRunes(c.Content, 1200)
		fmt.Fprintf(&b, "[%d] %s\n\n", i, strings.ReplaceAll(body, "\n", " "))
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := r.client.Chat(ctx, llm.Request{
		Model:     r.model,
		System:    rerankSystem,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: b.String()}},
		MaxTokens: 512,
	})
	if err != nil || resp == nil {
		return clamp(in, topK) // rerank is best-effort; keep retrieval order
	}
	scored := parseRerankJSON(resp.Content, len(in))
	if len(scored) == 0 {
		return clamp(in, topK)
	}
	out := make([]tools.RAGResult, 0, len(scored))
	for _, s := range scored {
		res := in[s.i]
		res.Score = s.score
		out = append(out, res)
	}
	return clamp(out, topK)
}

type rankItem struct {
	i     int
	score float64
}

// parseRerankJSON pulls the {"i","s"} array out of the model reply, tolerating a
// stray code fence or surrounding text, and drops out-of-range indices.
func parseRerankJSON(s string, n int) []rankItem {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '['); i >= 0 {
		if j := strings.LastIndexByte(s, ']'); j > i {
			s = s[i : j+1]
		}
	}
	var raw []struct {
		I int     `json:"i"`
		S float64 `json:"s"`
	}
	if json.Unmarshal([]byte(s), &raw) != nil {
		return nil
	}
	seen := map[int]bool{}
	var out []rankItem
	for _, r := range raw {
		if r.I < 0 || r.I >= n || seen[r.I] {
			continue
		}
		seen[r.I] = true
		out = append(out, rankItem{i: r.I, score: r.S})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out
}

// ---- API reranker (Voyage / Jina / Cohere shaped) ----------------------------

type apiReranker struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

func (r *apiReranker) rerank(ctx context.Context, query string, in []tools.RAGResult, topK int) []tools.RAGResult {
	if len(in) <= 1 {
		return clamp(in, topK)
	}
	docs := make([]string, len(in))
	for i, c := range in {
		docs[i] = c.Content
	}
	body, _ := json.Marshal(map[string]any{
		"query": query, "documents": docs, "model": r.model, "top_k": topK, "top_n": topK,
	})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return clamp(in, topK)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return clamp(in, topK)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return clamp(in, topK)
	}
	// Voyage/Cohere: {"results":[{"index":N,"relevance_score":x},...]};
	// Jina: {"results":[{"index":N,"relevance_score":x}]}. Same shape.
	var parsed struct {
		Results []struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		} `json:"results"`
		Data []struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return clamp(in, topK)
	}
	hits := parsed.Results
	if len(hits) == 0 {
		hits = parsed.Data
	}
	if len(hits) == 0 {
		return clamp(in, topK)
	}
	out := make([]tools.RAGResult, 0, len(hits))
	for _, h := range hits {
		if h.Index < 0 || h.Index >= len(in) {
			continue
		}
		res := in[h.Index]
		res.Score = h.Score
		out = append(out, res)
	}
	return clamp(out, topK)
}

func clamp(in []tools.RAGResult, topK int) []tools.RAGResult {
	if topK > 0 && len(in) > topK {
		return in[:topK]
	}
	return in
}
