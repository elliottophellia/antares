package llm

import (
	"strings"
	"testing"
)

func TestAzureEndpointRouting(t *testing.T) {
	c := &openAIClient{
		vendor: "azure",
		opts:   Options{BaseURL: "https://res.openai.azure.com", APIVersion: "2024-10-21"},
	}
	got := c.endpoint("/chat/completions", "gpt4o-deploy")
	want := "https://res.openai.azure.com/openai/deployments/gpt4o-deploy/chat/completions?api-version=2024-10-21"
	if got != want {
		t.Fatalf("azure endpoint = %q, want %q", got, want)
	}
}

func TestAzureUsesApiKeyHeader(t *testing.T) {
	c := &openAIClient{vendor: "azure", opts: Options{APIKey: "secret"}}
	h := c.headers()
	if h["api-key"] != "secret" {
		t.Fatalf("azure should use api-key header, got %v", h)
	}
	if _, ok := h["Authorization"]; ok {
		t.Fatal("azure must not send a bearer token")
	}
}

func TestOpenAIEndpointUnchanged(t *testing.T) {
	c := &openAIClient{vendor: "openai", opts: Options{BaseURL: "https://api.openai.com/v1"}}
	if got := c.endpoint("/chat/completions", "gpt-4o"); !strings.HasSuffix(got, "/v1/chat/completions") {
		t.Fatalf("non-azure endpoint changed: %q", got)
	}
}

func TestNewAzureRequiresBaseURL(t *testing.T) {
	if _, err := New(Options{Kind: "azure"}); err == nil {
		t.Fatal("azure without base_url should error")
	}
	c, err := New(Options{Kind: "azure", BaseURL: "https://res.openai.azure.com"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != "openai" {
		t.Fatalf("azure client should report openai dialect, got %s", c.Kind())
	}
}
