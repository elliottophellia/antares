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
	"github.com/enowdev/antares/internal/tools"
	"github.com/enowdev/antares/internal/version"
)

// enowxProvider talks to an enowx-rag daemon over its HTTP API
// (https://github.com/enowdev/enowx-rag), which owns the vector store,
// reranking, and near-duplicate compression.
type enowxProvider struct {
	baseURL string
	token   string
	cfg     *config.Config
	client  *http.Client
}

func newEnowxProvider(cfg *config.Config) *enowxProvider {
	return &enowxProvider{
		baseURL: strings.TrimRight(cfg.RAG.EnowxBaseURL, "/"),
		token:   cfg.RAG.EnowxToken,
		cfg:     cfg,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *enowxProvider) Name() string { return "enowx-rag" }

func (p *enowxProvider) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("enowx-rag unreachable at %s: %w", p.baseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		return fmt.Errorf("enowx-rag %s %s returned %d: %s", method, path, resp.StatusCode, msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (p *enowxProvider) Probe(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var projects any
	if err := p.do(ctx, "GET", "/api/projects", nil, &projects); err != nil {
		return false, err.Error()
	}
	return true, fmt.Sprintf("enowx-rag daemon at %s", p.baseURL)
}

type enowxSearchRequest struct {
	Project   string `json:"project,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Query     string `json:"query"`
	TopK      int    `json:"top_k,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Hybrid    bool   `json:"hybrid,omitempty"`
	Rerank    bool   `json:"rerank,omitempty"`
	Compress  bool   `json:"compress,omitempty"`
}

// enowxHit tolerates the field-name variations the daemon may use.
type enowxHit struct {
	Content string  `json:"content"`
	Text    string  `json:"text"`
	Chunk   string  `json:"chunk"`
	Path    string  `json:"path"`
	File    string  `json:"file"`
	DocID   string  `json:"doc_id"`
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
}

func (h enowxHit) normalise() tools.RAGResult {
	content := h.Content
	if content == "" {
		content = h.Text
	}
	if content == "" {
		content = h.Chunk
	}
	path := h.Path
	if path == "" {
		path = h.File
	}
	docID := h.DocID
	if docID == "" {
		docID = h.ID
	}
	return tools.RAGResult{Content: content, Path: path, DocID: docID, Score: h.Score}
}

func (p *enowxProvider) Search(ctx context.Context, collection, query string, topK int) ([]tools.RAGResult, error) {
	req := enowxSearchRequest{
		Project: collection, ProjectID: collection, Query: query,
		TopK: topK, Limit: topK, Hybrid: p.cfg.RAG.Hybrid,
		Rerank: p.cfg.RAG.EnowxRerank, Compress: p.cfg.RAG.EnowxCompress,
	}
	var raw struct {
		Results []enowxHit `json:"results"`
		Hits    []enowxHit `json:"hits"`
		Matches []enowxHit `json:"matches"`
	}
	if err := p.do(ctx, "POST", "/api/search", req, &raw); err != nil {
		return nil, err
	}
	hits := raw.Results
	if len(hits) == 0 {
		hits = raw.Hits
	}
	if len(hits) == 0 {
		hits = raw.Matches
	}
	out := make([]tools.RAGResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.normalise())
	}
	return out, nil
}

func (p *enowxProvider) Index(ctx context.Context, collection string, docs []tools.RAGDoc) (int, error) {
	// Make sure the project exists before pushing documents into it.
	_ = p.do(ctx, "POST", "/api/projects", map[string]any{"id": collection, "name": collection}, nil)

	payload := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		payload = append(payload, map[string]any{
			"id": d.ID, "path": d.Path, "content": d.Content, "metadata": d.Meta,
		})
	}
	var raw struct {
		Chunks  int `json:"chunks"`
		Indexed int `json:"indexed"`
		Count   int `json:"count"`
	}
	body := map[string]any{"project": collection, "project_id": collection, "documents": payload}
	if err := p.do(ctx, "POST", "/api/index", body, &raw); err != nil {
		return 0, err
	}
	for _, n := range []int{raw.Chunks, raw.Indexed, raw.Count} {
		if n > 0 {
			return n, nil
		}
	}
	return len(docs), nil
}

func (p *enowxProvider) Collections(ctx context.Context) ([]string, error) {
	var raw struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := p.do(ctx, "GET", "/api/projects", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw.Projects))
	for _, pr := range raw.Projects {
		id := pr.ID
		if id == "" {
			id = pr.Name
		}
		if id != "" {
			out = append(out, id)
		}
	}
	return out, nil
}

func (p *enowxProvider) Delete(ctx context.Context, collection string) error {
	return p.do(ctx, "DELETE", "/api/projects/"+collection, nil, nil)
}
