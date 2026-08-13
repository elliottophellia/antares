package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// pruneConfig sets the two knobs prunedToolResults reads. ProactivePruneMinChars
// is a character budget, like Tools.MaxOutputChars.
func pruneConfig(minChars int) *config.Config {
	cfg := config.Default()
	cfg.Compression.ProactivePruneMinChars = minChars
	cfg.Compression.ProtectLastN = 4
	return cfg
}

// historyWithToolResult puts one tool result ahead of a protected tail long
// enough for prunedToolResults to reach it.
func historyWithToolResult(content string) []llm.Message {
	h := []llm.Message{{Role: llm.RoleTool, ToolCallID: "c1", Name: "read_file", Content: content}}
	for i := 0; i < 8; i++ {
		h = append(h, llm.Message{Role: llm.RoleUser, Content: "turn"})
	}
	return h
}

// The pruned result and the notice that explains it must agree: the notice
// counts what was actually dropped, and what is kept is still valid UTF-8.
func TestPrunedToolResultsCountsCharactersNotBytes(t *testing.T) {
	const minChars = 100
	a := agentWithConfig(pruneConfig(minChars))
	content := strings.Repeat("字", 200) // 600 bytes

	got := a.prunedToolResults(historyWithToolResult(content))[0].Content

	if !utf8.ValidString(got) {
		t.Fatalf("pruned tool result is not valid UTF-8: %q", got)
	}
	kept, notice, found := strings.Cut(got, "\n\n[tool result pruned:")
	if !found {
		t.Fatalf("pruned tool result carries no notice: %q", got)
	}
	keptRunes := utf8.RuneCountInString(strings.TrimSuffix(kept, "…"))
	if keptRunes != minChars/2 {
		t.Fatalf("kept %d characters, want %d: %q", keptRunes, minChars/2, kept)
	}
	removed := utf8.RuneCountInString(content) - keptRunes
	want := fmt.Sprintf(" %d characters removed to free context]", removed)
	if notice != want {
		t.Fatalf("notice = %q, want %q — the number must be the characters actually removed", notice, want)
	}
}

// A result inside the minimum is left exactly as it is, however many bytes its
// characters happen to weigh.
func TestPrunedToolResultsLeavesResultsInsideTheMinimum(t *testing.T) {
	const minChars = 100
	a := agentWithConfig(pruneConfig(minChars))
	content := strings.Repeat("字", minChars) // 300 bytes, exactly the minimum

	if got := a.prunedToolResults(historyWithToolResult(content))[0].Content; got != content {
		t.Fatalf("a %d-character result inside the %d-character minimum was pruned to %q",
			utf8.RuneCountInString(content), minChars, got)
	}
}

// stubRAG answers with fixed hits per collection, so a test controls exactly
// what autoContext folds into its budget.
type stubRAG struct{ hits map[string][]tools.RAGResult }

func (stubRAG) Name() string { return "stub" }

func (s stubRAG) Search(_ context.Context, collection, _ string, _ int) ([]tools.RAGResult, error) {
	return s.hits[collection], nil
}

func (stubRAG) Index(context.Context, string, []tools.RAGDoc) (int, error) { return 0, nil }
func (stubRAG) Collections(context.Context) ([]string, error)              { return nil, nil }
func (stubRAG) Delete(context.Context, string) error                       { return nil }

// autoContextWith runs autoContext over the given bodies, all returned from the
// default knowledge collection.
func autoContextWith(bodies ...string) string {
	cfg := config.Default()
	cfg.RAG.AutoContext = true
	a := agentWithConfig(cfg)

	hits := make([]tools.RAGResult, 0, len(bodies))
	for _, body := range bodies {
		hits = append(hits, tools.RAGResult{Content: body})
	}
	a.rag = stubRAG{hits: map[string][]tools.RAGResult{"antares": hits}}

	return a.autoContext(context.Background(), Request{Message: "what did we decide?"}, &store.Session{ID: "s1"})
}

// The retrieval budget counts characters, so multi-byte bodies that fit inside
// it arrive whole rather than being cut to a third of their length.
func TestAutoContextBudgetCountsCharactersNotBytes(t *testing.T) {
	// 3000 characters in total, inside the 4000-character budget, but 9000
	// bytes — over it three times if the accumulator measures bytes.
	bodies := []string{
		strings.Repeat("あ", 1000),
		strings.Repeat("い", 1000),
		strings.Repeat("う", 1000),
	}
	block := autoContextWith(bodies...)

	if !utf8.ValidString(block) {
		t.Fatalf("auto-context block is not valid UTF-8: %q", block)
	}
	for i, body := range bodies {
		if !strings.Contains(block, body) {
			t.Fatalf("body %d (1000 characters, 3000 bytes) did not survive the 4000-character budget", i+1)
		}
	}
}

// A body that overruns the budget is cut on a character boundary, and only the
// characters that fit are charged to the budget.
func TestAutoContextTruncatesARetrievedBodyOnACharacterBoundary(t *testing.T) {
	block := autoContextWith(strings.Repeat("あ", 3000), strings.Repeat("い", 3000))

	if !utf8.ValidString(block) {
		t.Fatalf("auto-context block is not valid UTF-8: %q", block)
	}
	first, second := strings.Count(block, "あ"), strings.Count(block, "い")
	if first != 3000 {
		t.Fatalf("first body contributed %d characters, want 3000", first)
	}
	if second != 1000 {
		t.Fatalf("second body contributed %d characters, want the 1000 left in the budget", second)
	}
	if first+second != 4000 {
		t.Fatalf("retrieved %d characters, want the 4000-character budget", first+second)
	}
}
