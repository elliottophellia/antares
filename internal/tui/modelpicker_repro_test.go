package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/enowdev/antares/internal/config"
)

func TestModelPickerListsAllProviderModels(t *testing.T) {
	cfg := &config.Config{}
	cfg.Model.Default = "m1"
	cfg.Model.Provider = "custom"
	cfg.Providers = map[string]config.Provider{
		"custom": {Kind: "custom", Label: "Custom", Enabled: true, Models: []string{"m1", "m2"}},
	}
	m := &Model{cfg: cfg, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 24)
	m.openModelPicker()
	if len(m.picker.items) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(m.picker.items), m.picker.items)
	}
}

// The model picker is scoped to the active provider: a non-active provider's
// models must not appear.
func TestModelPickerActiveProviderOnly(t *testing.T) {
	cfg := &config.Config{}
	cfg.Model.Default = "gpt-x"
	cfg.Model.Provider = "openai"
	cfg.Providers = map[string]config.Provider{
		"openai": {Kind: "openai", Enabled: true, Models: []string{"gpt-x"}},
		"custom": {Kind: "custom", Enabled: true, Models: []string{"m1", "m2"}},
	}
	m := &Model{cfg: cfg, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 24)
	m.openModelPicker()
	if len(m.picker.items) != 1 {
		t.Fatalf("expected only the active provider's model, got %d: %+v", len(m.picker.items), m.picker.items)
	}
	if m.picker.items[0].id != "gpt-x" {
		t.Fatalf("expected gpt-x, got %q", m.picker.items[0].id)
	}
}

// Search filters the visible rows by substring on the label.
func TestPickerSearchFilters(t *testing.T) {
	cfg := &config.Config{}
	cfg.Model.Default = "claude-opus-5"
	cfg.Model.Provider = "anthropic"
	cfg.Providers = map[string]config.Provider{
		"anthropic": {Kind: "anthropic", Enabled: true,
			Models: []string{"claude-opus-5", "claude-sonnet-5", "gpt-ignored"}},
	}
	m := &Model{cfg: cfg, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 24)
	m.openModelPicker()
	m.picker.setQuery(m, "sonnet")
	if v := m.picker.vis(); len(v) != 1 || m.picker.items[v[0]].id != "claude-sonnet-5" {
		t.Fatalf("filter 'sonnet' should match one row, got %v", v)
	}
	m.picker.setQuery(m, "xyz")
	if v := m.picker.vis(); len(v) != 0 {
		t.Fatalf("filter 'xyz' should match nothing, got %v", v)
	}
}
