package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/engagement"
	"github.com/enowdev/antares/internal/findings"
)

func TestMethodologyBlockEmptyUntilEngagement(t *testing.T) {
	dir := t.TempDir()
	ag := &Agent{
		cfg:      config.Default(),
		intel:    engagement.NewStore(filepath.Join(dir, "intel")),
		findings: findings.NewStore(filepath.Join(dir, "findings")),
	}
	// No intel recorded yet: an ordinary session must see nothing.
	if block := ag.methodologyBlock("sess-1"); block != "" {
		t.Fatalf("expected empty block before any engagement, got %q", block)
	}

	// Once a fact is recorded, the assessment state should surface.
	if _, _, err := ag.intel.Add("sess-1", engagement.Intel{
		Type: engagement.NormalizeType("host"), Value: "example.com", Source: "recon",
	}); err != nil {
		t.Fatal(err)
	}
	block := ag.methodologyBlock("sess-1")
	if !strings.Contains(block, "Assessment in progress") {
		t.Fatalf("expected an assessment block after intel, got %q", block)
	}
	if !strings.Contains(block, "Next:") {
		t.Fatalf("expected a next-step directive, got %q", block)
	}
	// A different session stays clean.
	if block := ag.methodologyBlock("sess-2"); block != "" {
		t.Fatalf("engagement state must be per-session, got %q for sess-2", block)
	}
}
