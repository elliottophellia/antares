package llm

import (
	"strings"
	"testing"
)

func TestCopilotHeaders(t *testing.T) {
	h := copilotHeaders("cop-token-123")
	if h["Authorization"] != "Bearer cop-token-123" {
		t.Fatalf("bad auth header: %v", h)
	}
	for _, k := range []string{"Editor-Version", "Copilot-Integration-Id", "User-Agent"} {
		if h[k] == "" {
			t.Fatalf("missing required header %s", k)
		}
	}
}

func TestNewCopilotConstructs(t *testing.T) {
	c, err := New(Options{Kind: "copilot", APIKey: "gho_test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != "openai" {
		t.Fatalf("copilot should speak the openai dialect, got %s", c.Kind())
	}
	oc, ok := c.(*openAIClient)
	if !ok || oc.vendor != "copilot" || oc.copilot == nil {
		t.Fatalf("copilot client not wired: %+v", oc)
	}
	if oc.opts.BaseURL != "https://api.githubcopilot.com" {
		t.Fatalf("wrong default base url: %s", oc.opts.BaseURL)
	}
}

func TestCopilotTokenNeedsGitHubToken(t *testing.T) {
	ts := &copilotTokenSource{}
	if _, err := ts.token(); err == nil || !strings.Contains(err.Error(), "GitHub token") {
		t.Fatalf("empty token source should ask for a GitHub token, got %v", err)
	}
}
