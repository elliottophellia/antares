package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(context.Background(), "sqlite", dsn, 4, 5000, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sess := &Session{ID: "s1", Title: "Halo", Platform: "web", Model: "gpt-5"}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetSession(ctx, "s1")
	if err != nil || got.Title != "Halo" {
		t.Fatalf("get: %v %+v", err, got)
	}

	for i, body := range []string{"pertama tentang golang", "kedua tentang react"} {
		m := &Message{ID: string(rune('a'+i)) + "-msg", SessionID: "s1", Role: RoleUser, Content: body, TokensIn: 10}
		if err := s.AppendMessage(ctx, m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	msgs, err := s.ListMessages(ctx, "s1", 0, 0)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("list messages: %v n=%d", err, len(msgs))
	}
	if msgs[0].Seq != 1 || msgs[1].Seq != 2 {
		t.Fatalf("seq not monotonic: %d %d", msgs[0].Seq, msgs[1].Seq)
	}

	got, _ = s.GetSession(ctx, "s1")
	if got.MessageCount != 2 || got.TokensIn != 20 {
		t.Fatalf("counters not rolled up: %+v", got)
	}

	hits, err := s.SearchMessages(ctx, "react", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].MessageID != "b-msg" {
		t.Fatalf("fts miss: %+v", hits)
	}

	list, total, err := s.ListSessions(ctx, SessionFilter{Limit: 10})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list sessions: %v total=%d", err, total)
	}

	if err := s.DeleteSession(ctx, "s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetSession(ctx, "s1"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMemoryAndChunks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	m := &Memory{ID: "m1", Scope: "global", Key: "bahasa", Content: "User berbahasa Indonesia"}
	if err := s.PutMemory(ctx, m); err != nil {
		t.Fatalf("put memory: %v", err)
	}
	m.Content = "User memakai Bahasa Indonesia sehari-hari"
	if err := s.PutMemory(ctx, m); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	found, err := s.SearchMemories(ctx, "Indonesia", 10)
	if err != nil || len(found) != 1 {
		t.Fatalf("search memories: %v n=%d", err, len(found))
	}
	if found[0].Content != m.Content {
		t.Fatalf("fts stale after update: %q", found[0].Content)
	}

	chunks := []Chunk{
		{ID: "c1", Collection: "proj", Content: "golang http server", Embedding: []float32{1, 0, 0}},
		{ID: "c2", Collection: "proj", Content: "react component skeleton", Embedding: []float32{0, 1, 0}},
	}
	if err := s.PutChunks(ctx, chunks); err != nil {
		t.Fatalf("put chunks: %v", err)
	}
	res, scores, err := s.SearchChunks(ctx, "proj", []float32{0, 1, 0}, "react", 1, false)
	if err != nil || len(res) != 1 {
		t.Fatalf("search chunks: %v n=%d", err, len(res))
	}
	if res[0].ID != "c2" || scores[0] < 0.99 {
		t.Fatalf("wrong nearest: %s %v", res[0].ID, scores)
	}
}

func TestUsageAndKV(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	now := time.Now()
	for i := 0; i < 3; i++ {
		u := &Usage{ID: string(rune('x' + i)), Model: "gpt-5", Provider: "openai", TokensIn: 100, TokensOut: 50, Cost: 0.01, CreatedAt: now}
		if err := s.RecordUsage(ctx, u); err != nil {
			t.Fatalf("usage: %v", err)
		}
	}
	series, err := s.UsageSeries(ctx, now.Add(-time.Hour), "day")
	var calls int64
	for _, point := range series {
		calls += point.Calls
	}
	if err != nil || len(series) == 0 || calls != 3 {
		t.Fatalf("series: %v %+v", err, series)
	}
	byModel, err := s.UsageByModel(ctx, now.Add(-time.Hour))
	if err != nil || len(byModel) != 1 || byModel[0].TokensIn != 300 {
		t.Fatalf("by model: %v %+v", err, byModel)
	}

	if err := s.SetKV(ctx, "a.b", "1"); err != nil {
		t.Fatalf("setkv: %v", err)
	}
	if err := s.SetKV(ctx, "a.b", "2"); err != nil {
		t.Fatalf("setkv upsert: %v", err)
	}
	v, err := s.GetKV(ctx, "a.b")
	if err != nil || v != "2" {
		t.Fatalf("getkv: %v %q", err, v)
	}

	st, err := s.Stats(ctx)
	if err != nil || st.TokensIn != 300 {
		t.Fatalf("stats: %v %+v", err, st)
	}
}
