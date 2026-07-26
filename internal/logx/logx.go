// Package logx provides structured logging with an in-memory ring buffer so the
// dashboard can tail recent lines without touching the filesystem.
package logx

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Entry is one captured log record.
type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Source  string         `json:"source,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

const ringCapacity = 5000

type ring struct {
	mu   sync.RWMutex
	buf  []Entry
	next int
	full bool
	subs map[int]chan Entry
	seq  int
}

var buffer = &ring{buf: make([]Entry, ringCapacity), subs: map[int]chan Entry{}}

func (r *ring) push(e Entry) {
	r.mu.Lock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	subs := make([]chan Entry, 0, len(r.subs))
	for _, ch := range r.subs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default: // slow consumer: drop rather than stall the logger
		}
	}
}

// Tail returns up to n most recent entries, oldest first, optionally filtered.
func Tail(n int, level, contains string) []Entry {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()
	var all []Entry
	if buffer.full {
		all = append(all, buffer.buf[buffer.next:]...)
	}
	all = append(all, buffer.buf[:buffer.next]...)

	level = strings.ToUpper(strings.TrimSpace(level))
	contains = strings.ToLower(strings.TrimSpace(contains))
	out := make([]Entry, 0, len(all))
	for _, e := range all {
		if level != "" && level != "ALL" && !strings.EqualFold(e.Level, level) {
			continue
		}
		if contains != "" && !strings.Contains(strings.ToLower(e.Message), contains) {
			continue
		}
		out = append(out, e)
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Subscribe streams new entries until the returned cancel func runs.
func Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 256)
	buffer.mu.Lock()
	buffer.seq++
	id := buffer.seq
	buffer.subs[id] = ch
	buffer.mu.Unlock()
	return ch, func() {
		buffer.mu.Lock()
		if c, ok := buffer.subs[id]; ok {
			delete(buffer.subs, id)
			close(c)
		}
		buffer.mu.Unlock()
	}
}

// handler fans a record out to the ring buffer and a text/JSON writer.
type handler struct {
	inner slog.Handler
}

func (h *handler) Enabled(ctx contextT, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

func (h *handler) Handle(ctx contextT, r slog.Record) error {
	e := Entry{Time: r.Time, Level: r.Level.String(), Message: r.Message}
	if r.NumAttrs() > 0 {
		e.Attrs = make(map[string]any, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "source" {
				e.Source = fmt.Sprint(a.Value.Any())
				return true
			}
			e.Attrs[a.Key] = a.Value.Any()
			return true
		})
	}
	buffer.push(e)
	return h.inner.Handle(ctx, r)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	return &handler{inner: h.inner.WithAttrs(as)}
}
func (h *handler) WithGroup(n string) slog.Handler { return &handler{inner: h.inner.WithGroup(n)} }

// Setup installs the global logger. When path is non-empty, records are also
// appended to that file as JSON lines.
func Setup(level string, path string, jsonOut bool) error {
	lvl := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}

	var writers []io.Writer
	writers = append(writers, os.Stderr)
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		writers = append(writers, f)
	}
	w := io.MultiWriter(writers...)

	opts := &slog.HandlerOptions{Level: lvl}
	var inner slog.Handler
	if jsonOut {
		inner = slog.NewJSONHandler(w, opts)
	} else {
		inner = newConsoleHandler(w, lvl)
	}
	slog.SetDefault(slog.New(&handler{inner: inner}))
	return nil
}

// MarshalEntries is a helper for API responses.
func MarshalEntries(entries []Entry) ([]byte, error) { return json.Marshal(entries) }
