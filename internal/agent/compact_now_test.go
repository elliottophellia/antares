package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

func TestSplitForCompaction(t *testing.T) {
	msg := func(r, c string) llm.Message { return llm.Message{Role: r, Content: c} }

	t.Run("too short is not compactable", func(t *testing.T) {
		h := []llm.Message{msg(llm.RoleUser, "a"), msg(llm.RoleAssistant, "b")}
		if _, _, _, ok := splitForCompaction(h, 1, 4); ok {
			t.Fatal("a short history should not be compactable")
		}
	})

	t.Run("head, middle and tail cover the whole history", func(t *testing.T) {
		var h []llm.Message
		for i := 0; i < 12; i++ {
			h = append(h, msg(llm.RoleUser, "u"), msg(llm.RoleAssistant, "a"))
		}
		head, middle, tail, ok := splitForCompaction(h, 1, 4)
		if !ok {
			t.Fatal("expected a compactable history")
		}
		if len(head) != 1 {
			t.Fatalf("head = %d, want 1", len(head))
		}
		if len(tail) != 4 {
			t.Fatalf("tail = %d, want 4", len(tail))
		}
		if len(head)+len(middle)+len(tail) != len(h) {
			t.Fatalf("pieces %d+%d+%d do not cover %d", len(head), len(middle), len(tail), len(h))
		}
	})

	t.Run("tail never begins with an orphaned tool result", func(t *testing.T) {
		// With protectLast=3 the naive tail would open on the tool result whose
		// assistant call sits at the end of the middle; the rebalance must pull
		// that assistant across so the provider never sees a dangling result.
		h := []llm.Message{
			msg(llm.RoleUser, "1"), msg(llm.RoleAssistant, "2"),
			msg(llm.RoleUser, "3"), msg(llm.RoleAssistant, "4"),
			msg(llm.RoleUser, "5"),
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "t1", Name: "x"}}},
			{Role: llm.RoleTool, ToolCallID: "t1", Content: "out"},
			msg(llm.RoleUser, "8"), msg(llm.RoleAssistant, "9"),
		}
		_, _, tail, ok := splitForCompaction(h, 1, 3)
		if !ok {
			t.Fatal("expected a compactable history")
		}
		if len(tail) > 0 && tail[0].Role == llm.RoleTool {
			t.Fatalf("tail opens on an orphaned tool result: %#v", tail)
		}
	})
}

// CompactNow on a short session must not summarise or persist anything, but it
// must still report the current fill so the gauge stays accurate.
func TestCompactNowReportsNothingForShortSession(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.Default()
	cfg.Model.Default = "test-model"
	cfg.Model.Provider = "local"
	cfg.Model.ContextWindow = 200000
	cfg.Providers["local"] = config.Provider{Kind: "openai-compatible", BaseURL: "http://127.0.0.1:1", Enabled: true}
	a := &Agent{db: db, reg: tools.Default()}
	a.cfg.Store(cfg)

	if err := db.CreateSession(ctx, &store.Session{ID: "s1"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := db.AppendMessage(ctx, &store.Message{
			ID: fmt.Sprintf("m%d", i), SessionID: "s1", Role: store.RoleUser, Content: "hi",
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	var notices []string
	var usage *Event
	err = a.CompactNow(ctx, "s1", func(e Event) error {
		switch e.Type {
		case EventNotice:
			notices = append(notices, e.Message)
		case EventUsage:
			u := e
			usage = &u
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if usage == nil || usage.ContextWindow != 200000 {
		t.Fatalf("want a usage event carrying the window, got %+v", usage)
	}
	if !strings.Contains(strings.ToLower(strings.Join(notices, " ")), "nothing to compact") {
		t.Fatalf("want a 'nothing to compact' notice, got %v", notices)
	}
	fresh, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if _, ok := fresh.Meta[contextCompactMetaKey]; ok {
		t.Fatal("a short session must not receive a persisted compaction")
	}
}

// The summarising call is a real provider request; its cost must land in the
// usage totals so /usage and the cost readout account for compaction.
func TestRecordUsageSourceCountsCompaction(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	a := &Agent{db: db}
	a.cfg.Store(config.Default())

	a.recordUsageSource(ctx, "s1", "openai", "gpt-x",
		llm.Usage{InputTokens: 1200, OutputTokens: 300}, "compaction")

	rows, err := db.UsageByModel(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("usage by model: %v", err)
	}
	var in, out int64
	for _, r := range rows {
		in += r.TokensIn
		out += r.TokensOut
	}
	if in != 1200 || out != 300 {
		t.Fatalf("recorded usage in=%d out=%d, want 1200/300 — compaction cost was not counted", in, out)
	}
}
