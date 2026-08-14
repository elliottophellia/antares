package llm

import (
	"net/url"
	"strings"
)

// Official wire shapes. Custom / unknown hosts encode nothing.
const (
	wireOpenAIEffort      = "openai_effort"
	wireOpenRouter        = "openrouter"
	wireAnthropicBudget   = "anthropic_budget"
	wireAnthropicAdaptive = "anthropic_adaptive"
	wireGeminiBudget      = "gemini_budget"
	wireGeminiLevel       = "gemini_level"
	wireDeepSeek          = "deepseek"
	wireZai               = "zai"
	wireOllama            = "ollama"
)

// OfficialReasoningCapability is the native reasoning control for one official
// provider+model. Values are the strings that provider's API accepts.
type OfficialReasoningCapability struct {
	Wire   string   `json:"wire,omitempty"`
	Values []string `json:"values,omitempty"`
	// Default is the provider's documented default when the field is omitted.
	Default string `json:"default,omitempty"`
}

// AllowsOff reports whether the official API accepts a disable value.
func (c OfficialReasoningCapability) AllowsOff() bool {
	for _, v := range c.Values {
		if v == "none" {
			return true
		}
	}
	return false
}

// OfficialReasoning returns the native ladder for a known official endpoint.
// Any other host is a custom endpoint: the picker offers the full official
// value list and the request carries reasoning_effort as that string.
func OfficialReasoning(kind, baseURL, model string) OfficialReasoningCapability {
	kind = strings.ToLower(strings.TrimSpace(kind))
	model = strings.TrimSpace(model)
	host := officialHost(baseURL)
	if !isOfficialDeployment(kind, baseURL, host) {
		return customReasoning()
	}

	switch {
	case host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai"):
		return OfficialReasoningCapability{
			Wire:    wireOpenRouter,
			Values:  []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
			Default: "medium",
		}
	case host == "api.x.ai" || host == "x.ai" || kind == "xai":
		return xaiReasoning(model)
	case host == "api.groq.com" || strings.HasSuffix(host, ".groq.com"):
		return groqReasoning(model)
	case host == "api.deepseek.com" || host == "deepseek.com":
		return deepseekReasoning()
	case host == "api.z.ai" || host == "z.ai" || strings.HasSuffix(host, ".z.ai"):
		return zaiReasoning(model)
	case isOllamaURL(baseURL, host):
		return ollamaReasoning(model)
	}

	switch kind {
	case "anthropic":
		return anthropicReasoning(model)
	case "openai", "azure", "azure-openai", "azureopenai", "codex", "responses", "openai-responses":
		return openaiReasoning(model)
	case "gemini":
		return geminiReasoning(model)
	case "opencode":
		return opencodeReasoning(model)
	default:
		return customReasoning()
	}
}

// customReasoning is the user-facing ladder for any non-official endpoint.
// Auto (empty) still omits the field. medium is included because every
// official API that has a mid rung uses that name.
func customReasoning() OfficialReasoningCapability {
	return OfficialReasoningCapability{
		Wire:   wireOpenAIEffort,
		Values: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
	}
}

func isOfficialDeployment(kind, baseURL, host string) bool {
	if officialKnownHost(host) || isOllamaURL(baseURL, host) {
		return true
	}
	if host != "" {
		return false
	}
	switch kind {
	case "anthropic", "openai", "gemini", "opencode", "xai", "codex", "responses", "openai-responses":
		return true
	default:
		return false
	}
}

func officialKnownHost(host string) bool {
	switch {
	case host == "api.openai.com", host == "openai.com":
		return true
	case host == "api.anthropic.com", host == "anthropic.com":
		return true
	case host == "generativelanguage.googleapis.com", strings.HasSuffix(host, ".googleapis.com"):
		return true
	case host == "openrouter.ai", strings.HasSuffix(host, ".openrouter.ai"):
		return true
	case host == "api.x.ai", host == "x.ai":
		return true
	case host == "api.groq.com", strings.HasSuffix(host, ".groq.com"):
		return true
	case host == "api.deepseek.com", host == "deepseek.com":
		return true
	case host == "api.z.ai", host == "z.ai", strings.HasSuffix(host, ".z.ai"):
		return true
	case host == "opencode.ai", strings.HasSuffix(host, ".opencode.ai"):
		return true
	case strings.HasSuffix(host, ".openai.azure.com"):
		return true
	default:
		return false
	}
}

// EncodedReasoning is the official request fragment for one chosen value.
type EncodedReasoning struct {
	Omit           bool
	Body           map[string]any
	GeminiThinking map[string]any
}

// Encode maps a user-selected official value onto the provider request.
// Empty effort is Auto: omit the field so the provider default applies.
// Values the model does not advertise are omitted rather than remapped.
func (c OfficialReasoningCapability) Encode(effort string) EncodedReasoning {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if c.Wire == "" || len(c.Values) == 0 {
		return EncodedReasoning{Omit: true}
	}
	if effort == "" {
		return EncodedReasoning{Omit: true}
	}
	if !c.allows(effort) {
		return EncodedReasoning{Omit: true}
	}
	switch c.Wire {
	case wireOpenAIEffort:
		return EncodedReasoning{Body: map[string]any{"reasoning_effort": effort}}
	case wireOpenRouter:
		if effort == "none" {
			return EncodedReasoning{Body: map[string]any{"reasoning": map[string]any{"effort": "none"}}}
		}
		return EncodedReasoning{Body: map[string]any{"reasoning": map[string]any{"effort": effort}}}
	case wireAnthropicAdaptive:
		return EncodedReasoning{Body: map[string]any{
			"thinking":      map[string]any{"type": "adaptive"},
			"output_config": map[string]any{"effort": effort},
		}}
	case wireAnthropicBudget:
		if effort == "none" {
			return EncodedReasoning{Omit: true}
		}
		return EncodedReasoning{Body: map[string]any{
			"thinking": map[string]any{"type": "enabled", "budget_tokens": anthropicBudget(effort)},
		}}
	case wireGeminiLevel:
		level := strings.ToUpper(effort)
		return EncodedReasoning{GeminiThinking: map[string]any{
			"thinkingLevel":   level,
			"includeThoughts": effort != "minimal",
		}}
	case wireGeminiBudget:
		if effort == "none" {
			return EncodedReasoning{GeminiThinking: map[string]any{"thinkingBudget": 0}}
		}
		return EncodedReasoning{GeminiThinking: map[string]any{
			"thinkingBudget":  geminiBudget(effort),
			"includeThoughts": true,
		}}
	case wireDeepSeek:
		if effort == "none" {
			return EncodedReasoning{Body: map[string]any{"thinking": map[string]any{"type": "disabled"}}}
		}
		return EncodedReasoning{Body: map[string]any{
			"thinking":         map[string]any{"type": "enabled"},
			"reasoning_effort": effort,
		}}
	case wireZai:
		if effort == "none" {
			return EncodedReasoning{Body: map[string]any{"thinking": map[string]any{"type": "disabled"}}}
		}
		return EncodedReasoning{Body: map[string]any{
			"thinking":         map[string]any{"type": "enabled"},
			"reasoning_effort": effort,
		}}
	case wireOllama:
		if effort == "none" {
			return EncodedReasoning{Body: map[string]any{"think": false}}
		}
		return EncodedReasoning{Body: map[string]any{"think": effort}}
	default:
		return EncodedReasoning{Omit: true}
	}
}

func (c OfficialReasoningCapability) allows(effort string) bool {
	for _, v := range c.Values {
		if v == effort {
			return true
		}
	}
	return false
}

func officialHost(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func isOllamaURL(baseURL, host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
	default:
		return false
	}
	return strings.Contains(baseURL, ":11434")
}

func openaiReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "gpt-5-chat"):
		return OfficialReasoningCapability{}
	case strings.Contains(m, "gpt-5.6"), strings.Contains(m, "gpt-5.5"),
		strings.Contains(m, "gpt-5.4"), strings.Contains(m, "gpt-5.3"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"none", "low", "medium", "high", "xhigh", "max"},
			Default: "medium",
		}
	case strings.Contains(m, "gpt-5.1"), strings.Contains(m, "gpt-5-codex"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"none", "low", "medium", "high"},
			Default: "none",
		}
	case strings.Contains(m, "gpt-5"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"minimal", "low", "medium", "high"},
			Default: "medium",
		}
	case strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"low", "medium", "high"},
			Default: "medium",
		}
	default:
		return OfficialReasoningCapability{}
	}
}

func anthropicReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(model)
	if strings.Contains(m, "4-6") || strings.Contains(m, "4.6") ||
		strings.Contains(m, "4-7") || strings.Contains(m, "4.7") ||
		strings.Contains(m, "4-8") || strings.Contains(m, "4.8") ||
		strings.Contains(m, "opus-5") || strings.Contains(m, "sonnet-5") ||
		strings.Contains(m, "haiku-5") || strings.Contains(m, "claude-5") {
		return OfficialReasoningCapability{
			Wire:    wireAnthropicAdaptive,
			Values:  []string{"low", "medium", "high", "max"},
			Default: "high",
		}
	}
	if strings.Contains(m, "claude") || strings.Contains(m, "opus") ||
		strings.Contains(m, "sonnet") || strings.Contains(m, "haiku") {
		return OfficialReasoningCapability{
			Wire:    wireAnthropicBudget,
			Values:  []string{"none", "low", "medium", "high"},
			Default: "",
		}
	}
	return OfficialReasoningCapability{}
}

func geminiReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(strings.TrimPrefix(model, "models/"))
	switch {
	case strings.Contains(m, "gemini-3.1-flash-lite-image") || strings.Contains(m, "gemini-3.1-flash-lite"):
		return OfficialReasoningCapability{
			Wire:    wireGeminiLevel,
			Values:  []string{"minimal", "high"},
			Default: "minimal",
		}
	case strings.Contains(m, "gemini-3.6"):
		return OfficialReasoningCapability{
			Wire:    wireGeminiLevel,
			Values:  []string{"minimal", "low", "medium", "high"},
			Default: "medium",
		}
	case strings.Contains(m, "gemini-3.7"):
		return OfficialReasoningCapability{
			Wire:    wireGeminiLevel,
			Values:  []string{"low", "medium", "high"},
			Default: "medium",
		}
	case strings.Contains(m, "gemini-3.5-flash-lite"):
		return OfficialReasoningCapability{
			Wire:    wireGeminiLevel,
			Values:  []string{"minimal", "low", "medium", "high"},
			Default: "minimal",
		}
	case strings.Contains(m, "gemini-3.1-pro") || strings.Contains(m, "gemini-3-pro"):
		return OfficialReasoningCapability{
			Wire:    wireGeminiLevel,
			Values:  []string{"low", "medium", "high"},
			Default: "high",
		}
	case strings.HasPrefix(m, "gemini-3") || strings.Contains(m, "gemini-3."):
		return OfficialReasoningCapability{
			Wire:    wireGeminiLevel,
			Values:  []string{"low", "medium", "high"},
			Default: "high",
		}
	case strings.Contains(m, "gemini-2.5-pro"):
		return OfficialReasoningCapability{
			Wire:    wireGeminiBudget,
			Values:  []string{"low", "medium", "high"},
			Default: "",
		}
	case strings.Contains(m, "gemini-2.5"):
		return OfficialReasoningCapability{
			Wire:    wireGeminiBudget,
			Values:  []string{"none", "low", "medium", "high"},
			Default: "",
		}
	default:
		return OfficialReasoningCapability{}
	}
}

func xaiReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "grok-4.6"), strings.Contains(m, "grok-4-6"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"low", "medium", "high", "xhigh"},
			Default: "high",
		}
	case strings.Contains(m, "4.20-multi-agent") || strings.Contains(m, "4-20-multi-agent"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"low", "medium", "high", "xhigh"},
			Default: "high",
		}
	case strings.Contains(m, "grok-4.5"), strings.Contains(m, "grok-4-5"),
		m == "grok", strings.Contains(m, "grok-latest"), strings.Contains(m, "grok-build"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"low", "medium", "high"},
			Default: "high",
		}
	case strings.Contains(m, "grok-3-mini"):
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"low", "medium", "high"},
			Default: "high",
		}
	default:
		return OfficialReasoningCapability{}
	}
}

func groqReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(model)
	if strings.Contains(m, "gpt-oss") {
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"low", "medium", "high"},
			Default: "medium",
		}
	}
	if strings.Contains(m, "qwen") {
		return OfficialReasoningCapability{
			Wire:    wireOpenAIEffort,
			Values:  []string{"none", "default"},
			Default: "default",
		}
	}
	return OfficialReasoningCapability{}
}

func deepseekReasoning() OfficialReasoningCapability {
	return OfficialReasoningCapability{
		Wire:    wireDeepSeek,
		Values:  []string{"none", "low", "high", "max"},
		Default: "high",
	}
}

func zaiReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(model)
	if strings.Contains(m, "glm-5.2") || strings.Contains(m, "glm-5-2") ||
		strings.Contains(m, "glm-5.1") || strings.Contains(m, "glm-5.") {
		return OfficialReasoningCapability{
			Wire:    wireZai,
			Values:  []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
			Default: "max",
		}
	}
	return OfficialReasoningCapability{
		Wire:    wireZai,
		Values:  []string{"none"},
		Default: "",
	}
}

func ollamaReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(model)
	if strings.Contains(m, "gpt-oss") {
		return OfficialReasoningCapability{
			Wire:    wireOllama,
			Values:  []string{"low", "medium", "high"},
			Default: "medium",
		}
	}
	return OfficialReasoningCapability{
		Wire:    wireOllama,
		Values:  []string{"none", "low", "medium", "high", "max"},
		Default: "",
	}
}

func opencodeReasoning(model string) OfficialReasoningCapability {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "glm"):
		return zaiReasoning(m)
	case strings.Contains(m, "deepseek"):
		return deepseekReasoning()
	default:
		return OfficialReasoningCapability{}
	}
}

func anthropicBudget(effort string) int {
	switch effort {
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 16384
	default:
		return 8192
	}
}

func geminiBudget(effort string) int {
	switch effort {
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 24576
	default:
		return 8192
	}
}

func mergeEncoded(body map[string]any, enc EncodedReasoning) {
	if enc.Omit {
		return
	}
	for k, v := range enc.Body {
		body[k] = v
	}
}
