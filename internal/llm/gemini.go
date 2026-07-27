package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// geminiClient speaks Google's generateContent API, either the public
// Generative Language endpoint (api-key auth) or Vertex AI on GCP (OAuth
// service-account auth, project/location URLs).
type geminiClient struct {
	opts    Options
	vertex  bool
	project string
	region  string
	tokens  *gcpTokenSource
}

func (c *geminiClient) Kind() string {
	if c.vertex {
		return "vertex"
	}
	return "gemini"
}

func (c *geminiClient) headers() map[string]string {
	h := map[string]string{}
	if c.vertex {
		if tok, err := c.tokens.token(); err == nil && tok != "" {
			h["Authorization"] = "Bearer " + tok
		}
		return h
	}
	if c.opts.APIKey != "" {
		h["x-goog-api-key"] = c.opts.APIKey
	}
	return h
}

type gemPart struct {
	Text             string         `json:"text,omitempty"`
	Thought          bool           `json:"thought,omitempty"`
	InlineData       *gemInlineData `json:"inlineData,omitempty"`
	FunctionCall     *gemFuncCall   `json:"functionCall,omitempty"`
	FunctionResponse *gemFuncResult `json:"functionResponse,omitempty"`
}

type gemInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gemFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gemFuncResult struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"` // user|model
	Parts []gemPart `json:"parts"`
}

func toGemini(req Request) []gemContent {
	var out []gemContent
	// Tool calls are keyed by id upstream but by name in Gemini; keep a map so
	// tool results can be matched back to their call.
	callName := map[string]string{}

	appendMsg := func(role string, parts []gemPart) {
		if len(parts) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Parts = append(out[n-1].Parts, parts...)
			return
		}
		out = append(out, gemContent{Role: role, Parts: parts})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			continue
		case RoleAssistant:
			var parts []gemPart
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, gemPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				callName[tc.ID] = tc.Name
				args := json.RawMessage(tc.Arguments)
				if strings.TrimSpace(tc.Arguments) == "" {
					args = json.RawMessage("{}")
				}
				parts = append(parts, gemPart{FunctionCall: &gemFuncCall{Name: tc.Name, Args: args}})
			}
			appendMsg("model", parts)
		case RoleTool:
			name := m.ToolName()
			if n, ok := callName[m.ToolCallID]; ok && n != "" {
				name = n
			}
			appendMsg("user", []gemPart{{FunctionResponse: &gemFuncResult{
				Name:     name,
				Response: map[string]any{"result": m.Content},
			}}})
		default:
			var parts []gemPart
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, gemPart{Text: m.Content})
			}
			for _, p := range m.Parts {
				switch p.Type {
				case "text":
					parts = append(parts, gemPart{Text: p.Text})
				case "image":
					if p.Data != "" {
						mime := p.MimeType
						if mime == "" {
							mime = "image/png"
						}
						parts = append(parts, gemPart{InlineData: &gemInlineData{MimeType: mime, Data: p.Data}})
					}
				}
			}
			appendMsg("user", parts)
		}
	}
	return out
}

// sanitizeSchema strips JSON Schema keywords Gemini rejects.
func sanitizeSchema(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch k {
		case "additionalProperties", "$schema", "$id", "default", "examples", "title", "exclusiveMinimum", "exclusiveMaximum":
			continue
		}
		switch tv := v.(type) {
		case map[string]any:
			out[k] = sanitizeSchema(tv)
		case []any:
			arr := make([]any, 0, len(tv))
			for _, item := range tv {
				if m, ok := item.(map[string]any); ok {
					arr = append(arr, sanitizeSchema(m))
					continue
				}
				arr = append(arr, item)
			}
			out[k] = arr
		default:
			out[k] = v
		}
	}
	if _, ok := out["type"]; !ok {
		if _, hasProps := out["properties"]; hasProps {
			out["type"] = "object"
		}
	}
	return out
}

func (c *geminiClient) buildBody(req Request) map[string]any {
	body := map[string]any{"contents": toGemini(req)}
	if s := strings.TrimSpace(req.System); s != "" {
		body["systemInstruction"] = gemContent{Parts: []gemPart{{Text: s}}}
	}
	gen := map[string]any{}
	if req.Temperature > 0 {
		gen["temperature"] = req.Temperature
	}
	if req.TopP > 0 && req.TopP < 1 {
		gen["topP"] = req.TopP
	}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		gen["stopSequences"] = req.StopSequences
	}
	switch strings.ToLower(req.ReasoningEffort) {
	case "low":
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": 2048, "includeThoughts": true}
	case "medium":
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": 8192, "includeThoughts": true}
	case "high":
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": 24576, "includeThoughts": true}
	case "none":
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": 0}
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  sanitizeSchema(t.Parameters),
			})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
		mode := "AUTO"
		switch req.ToolChoice {
		case "none":
			mode = "NONE"
		case "required":
			mode = "ANY"
		}
		body["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": mode}}
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return body
}

type gemResponse struct {
	Candidates []struct {
		Content      gemContent `json:"content"`
		FinishReason string     `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
		ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *geminiClient) endpoint(model, method string, stream bool) string {
	model = strings.TrimPrefix(model, "models/")
	if c.vertex {
		u := fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
			c.opts.BaseURL, c.project, c.region, url.PathEscape(model), method)
		if stream {
			u += "?alt=sse"
		}
		return u
	}
	u := c.opts.BaseURL + "/models/" + url.PathEscape(model) + ":" + method
	if stream {
		u += "?alt=sse"
	}
	return u
}

func (c *geminiClient) Chat(ctx context.Context, req Request) (*Response, error) {
	var raw gemResponse
	if err := c.opts.doJSON(ctx, "POST", c.endpoint(req.Model, "generateContent", false), c.buildBody(req), c.headers(), &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("provider error: %s", raw.Error.Message)
	}
	resp := &Response{Model: req.Model}
	if len(raw.Candidates) > 0 {
		cand := raw.Candidates[0]
		resp.FinishReason = strings.ToLower(cand.FinishReason)
		var text, thought strings.Builder
		for i, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				args := string(p.FunctionCall.Args)
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				resp.ToolCalls = append(resp.ToolCalls, ToolCall{
					ID: fmt.Sprintf("call_%d_%s", i, p.FunctionCall.Name), Name: p.FunctionCall.Name, Arguments: args,
				})
			case p.Thought:
				thought.WriteString(p.Text)
			default:
				text.WriteString(p.Text)
			}
		}
		resp.Content, resp.Reasoning = text.String(), thought.String()
	}
	if u := raw.UsageMetadata; u != nil {
		resp.Usage = Usage{
			InputTokens: u.PromptTokenCount, OutputTokens: u.CandidatesTokenCount,
			CacheReadTokens: u.CachedContentTokenCount, ReasoningTokens: u.ThoughtsTokenCount,
		}
	}
	return resp, nil
}

func (c *geminiClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	httpResp, err := c.opts.do(ctx, "POST", c.endpoint(req.Model, "streamGenerateContent", true), c.buildBody(req), c.headers())
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var (
		text    strings.Builder
		thought strings.Builder
		calls   []ToolCall
		usage   Usage
		finish  string
		callSeq int
		emitted = map[string]bool{}
	)

	err = sseLines(httpResp.Body, func(_, data string) error {
		var raw gemResponse
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			return nil
		}
		if raw.Error != nil {
			return fmt.Errorf("provider error: %s", raw.Error.Message)
		}
		if u := raw.UsageMetadata; u != nil {
			usage = Usage{
				InputTokens: u.PromptTokenCount, OutputTokens: u.CandidatesTokenCount,
				CacheReadTokens: u.CachedContentTokenCount, ReasoningTokens: u.ThoughtsTokenCount,
			}
			cp := usage
			if err := emit(Event{Type: EventUsage, Usage: &cp}); err != nil {
				return err
			}
		}
		for _, cand := range raw.Candidates {
			if cand.FinishReason != "" {
				finish = strings.ToLower(cand.FinishReason)
			}
			for _, p := range cand.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					args := string(p.FunctionCall.Args)
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					id := fmt.Sprintf("call_%d_%s", callSeq, p.FunctionCall.Name)
					callSeq++
					if emitted[id] {
						continue
					}
					emitted[id] = true
					idx := len(calls)
					calls = append(calls, ToolCall{ID: id, Name: p.FunctionCall.Name, Arguments: args})
					if err := emit(Event{Type: EventToolCallStart, Index: idx, ToolCallID: id, ToolName: p.FunctionCall.Name}); err != nil {
						return err
					}
					if err := emit(Event{Type: EventToolCallDelta, Index: idx, ToolCallID: id, ToolName: p.FunctionCall.Name, Delta: args}); err != nil {
						return err
					}
				case p.Thought:
					thought.WriteString(p.Text)
					if err := emit(Event{Type: EventReasoning, Delta: p.Text}); err != nil {
						return err
					}
				case p.Text != "":
					text.WriteString(p.Text)
					if err := emit(Event{Type: EventText, Delta: p.Text}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i, call := range calls {
		if err := emit(Event{Type: EventToolCallEnd, Index: i, ToolCallID: call.ID, ToolName: call.Name, Delta: call.Arguments}); err != nil {
			return nil, err
		}
	}
	return &Response{
		Content: text.String(), Reasoning: thought.String(), ToolCalls: calls,
		FinishReason: finish, Model: req.Model, Usage: usage,
	}, nil
}

func (c *geminiClient) Models(ctx context.Context) ([]ModelInfo, error) {
	if c.vertex {
		// Vertex lists publisher models through a different API; users name the
		// model directly.
		return nil, nil
	}
	var raw struct {
		Models []struct {
			Name             string   `json:"name"`
			DisplayName      string   `json:"displayName"`
			InputTokenLimit  int      `json:"inputTokenLimit"`
			OutputTokenLimit int      `json:"outputTokenLimit"`
			Methods          []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := c.opts.doJSON(ctx, "GET", c.opts.BaseURL+"/models?pageSize=200", nil, c.headers(), &raw); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(raw.Models))
	for _, m := range raw.Models {
		supported := false
		for _, meth := range m.Methods {
			if meth == "generateContent" {
				supported = true
			}
		}
		if !supported {
			continue
		}
		id := strings.TrimPrefix(m.Name, "models/")
		out = append(out, ModelInfo{
			ID: id, Name: firstNonEmpty(m.DisplayName, id), Provider: c.opts.ProviderID,
			ContextWindow: m.InputTokenLimit, MaxOutput: m.OutputTokenLimit,
			Vision: true, Tools: true, Reasoning: strings.Contains(id, "2.5") || strings.Contains(id, "3"),
		})
	}
	return out, nil
}

func (c *geminiClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if model == "" {
		model = "text-embedding-004"
	}
	reqs := make([]map[string]any, 0, len(inputs))
	for _, in := range inputs {
		reqs = append(reqs, map[string]any{
			"model":   "models/" + strings.TrimPrefix(model, "models/"),
			"content": map[string]any{"parts": []map[string]any{{"text": in}}},
		})
	}
	var raw struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	body := map[string]any{"requests": reqs}
	if err := c.opts.doJSON(ctx, "POST", c.endpoint(model, "batchEmbedContents", false), body, c.headers(), &raw); err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(raw.Embeddings))
	for _, e := range raw.Embeddings {
		out = append(out, e.Values)
	}
	return out, nil
}
