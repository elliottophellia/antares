package config

import "testing"

// A config written while Cursor Cloud Agents still shipped must not survive
// normalization: left in place the entry would fall through to the
// OpenAI-compatible adapter and fail opaquely against api.cursor.com.
func TestNormalizeDropsRetiredCursorProvider(t *testing.T) {
	c := Default()
	c.Providers["cursor"] = Provider{
		Kind: "cursor-agent", Label: "Cursor Cloud Agents", Enabled: true,
		BaseURL: "https://api.cursor.com", APIKey: "synthetic-key",
	}
	c.Model.Provider = "cursor"
	c.Tools.Timeouts["cursor_agent"] = 960
	c.Tools.Timeouts["cursor_agent_status"] = 960

	Normalize(c)

	if _, ok := c.Providers["cursor"]; ok {
		t.Error("retired cursor provider survived normalization")
	}
	if c.Model.Provider != "" {
		t.Errorf("active provider = %q, want it cleared so setup picks a real one", c.Model.Provider)
	}
	for _, name := range []string{"cursor_agent", "cursor_agent_status"} {
		if _, ok := c.Tools.Timeouts[name]; ok {
			t.Errorf("timeout for removed tool %q survived", name)
		}
	}
}

// A provider that merely happens to be named "cursor" but is an ordinary
// OpenAI-compatible endpoint is the user's own; only the retired kind goes.
func TestNormalizeKeepsUnrelatedProviderNamedCursor(t *testing.T) {
	c := Default()
	c.Providers["cursor"] = Provider{
		Kind: "openai-compatible", BaseURL: "https://example.internal/v1", Enabled: true,
	}
	c.Model.Provider = "cursor"

	Normalize(c)

	if _, ok := c.Providers["cursor"]; !ok {
		t.Fatal("an ordinary provider named cursor was removed")
	}
	if c.Model.Provider != "cursor" {
		t.Errorf("active provider = %q, want it left alone", c.Model.Provider)
	}
}
