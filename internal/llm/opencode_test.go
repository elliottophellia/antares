package llm

import "testing"

// OpenCode serves two wire formats from one base URL. Routing the wrong way is
// silent and total: an Anthropic-format model sent to /chat/completions with a
// Bearer header fails auth and shape at once.
func TestOpenCodeRoutesModelsToTheRightWireFormat(t *testing.T) {
	messagesAPI := []string{
		"minimax-m3",
		"minimax-m2.7",
		"minimax-m2.5",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.6-plus",
	}
	for _, m := range messagesAPI {
		if !openCodeUsesMessagesAPI(m) {
			t.Errorf("%s must use the Anthropic /messages API", m)
		}
	}

	chatCompletions := []string{
		"glm-5.2",
		"glm-5.1",
		"kimi-k2.7-code",
		"kimi-k2.6",
		"deepseek-v4-pro",
		"deepseek-v4-flash",
		"mimo-v2.5",
		"mimo-v2.5-pro",
	}
	for _, m := range chatCompletions {
		if openCodeUsesMessagesAPI(m) {
			t.Errorf("%s must use the OpenAI /chat/completions API", m)
		}
	}
}

// A dated or suffixed variant of a known family must keep its wire format —
// matching on the exact id would silently mis-route the next model revision.
func TestOpenCodeRoutingToleratesVariants(t *testing.T) {
	for _, m := range []string{"minimax-m3-0711", "qwen3.7-max-thinking", "MiniMax-M3"} {
		if !openCodeUsesMessagesAPI(m) {
			t.Errorf("variant %s should still route to /messages", m)
		}
	}
	if openCodeUsesMessagesAPI("glm-5.2-flash") {
		t.Error("glm variant must not route to /messages")
	}
}

// The agent may hand the adapter a "provider/model" spec; the prefix must not
// hide the model family.
func TestOpenCodeRoutingStripsProviderPrefix(t *testing.T) {
	if !openCodeUsesMessagesAPI("opencode/minimax-m3") {
		t.Error("prefixed minimax should route to /messages")
	}
	if openCodeUsesMessagesAPI("opencode/glm-5.2") {
		t.Error("prefixed glm should route to /chat/completions")
	}
}

// Each underlying adapter must get the shared base URL, and must not share a
// headers map (one adapter's auth header would leak into the other).
func TestOpenCodeBuildsBothAdapters(t *testing.T) {
	c, err := newOpenCode(Options{APIKey: "sk-test", Headers: map[string]string{"X-Trace": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	oc, ok := c.(*openCodeClient)
	if !ok {
		t.Fatalf("expected *openCodeClient, got %T", c)
	}
	if oc.anthropic.opts.BaseURL != openCodeDefaultBaseURL {
		t.Errorf("anthropic base = %q, want %q", oc.anthropic.opts.BaseURL, openCodeDefaultBaseURL)
	}
	if oc.openai.opts.BaseURL != openCodeDefaultBaseURL {
		t.Errorf("openai base = %q, want %q", oc.openai.opts.BaseURL, openCodeDefaultBaseURL)
	}
	oc.anthropic.opts.Headers["X-Trace"] = "mutated"
	if oc.openai.opts.Headers["X-Trace"] != "1" {
		t.Error("adapters share a headers map; mutating one changed the other")
	}
	if oc.Kind() != "opencode" {
		t.Errorf("Kind() = %q", oc.Kind())
	}
}

// An explicit base_url (self-hosted or a proxy in front of Zen) must win over
// the default.
func TestOpenCodeHonoursExplicitBaseURL(t *testing.T) {
	c, err := newOpenCode(Options{BaseURL: "http://127.0.0.1:20128/v1/", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	oc := c.(*openCodeClient)
	if oc.openai.opts.BaseURL != "http://127.0.0.1:20128/v1" {
		t.Errorf("base = %q, want trailing slash trimmed and value preserved", oc.openai.opts.BaseURL)
	}
}

// New() must resolve every alias to the routing adapter, not fall through to
// the generic OpenAI-compatible one.
func TestNewResolvesOpenCodeAliases(t *testing.T) {
	for _, kind := range []string{"opencode", "opencode-go", "opencode-zen", "zen"} {
		c, err := New(Options{Kind: kind, APIKey: "k"})
		if err != nil {
			t.Fatalf("kind %q: %v", kind, err)
		}
		if c.Kind() != "opencode" {
			t.Errorf("kind %q resolved to %q, want the opencode router", kind, c.Kind())
		}
	}
}
