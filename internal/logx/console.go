package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

type contextT = context.Context

const (
	ansiReset = "\033[0m"
	ansiDim   = "\033[2m"
	ansiRed   = "\033[31m"
	ansiYell  = "\033[33m"
	ansiCyan  = "\033[36m"
	ansiGrey  = "\033[90m"
)

// consoleHandler renders human-readable single-line records.
type consoleHandler struct {
	mu     *sync.Mutex
	w      io.Writer
	level  slog.Level
	attrs  []slog.Attr
	groups []string
}

func newConsoleHandler(w io.Writer, level slog.Level) slog.Handler {
	return &consoleHandler{mu: &sync.Mutex{}, w: w, level: level}
}

func (h *consoleHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return ansiRed
	case l >= slog.LevelWarn:
		return ansiYell
	case l <= slog.LevelDebug:
		return ansiGrey
	default:
		return ansiCyan
	}
}

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(ansiDim)
	b.WriteString(r.Time.Format("15:04:05.000"))
	b.WriteString(ansiReset + " ")
	b.WriteString(levelColor(r.Level))
	fmt.Fprintf(&b, "%-5s", strings.ToUpper(r.Level.String()))
	b.WriteString(ansiReset + " ")
	b.WriteString(r.Message)

	write := func(a slog.Attr) {
		key := a.Key
		if len(h.groups) > 0 {
			key = strings.Join(h.groups, ".") + "." + key
		}
		fmt.Fprintf(&b, " %s%s=%v%s", ansiDim, key, a.Value.Any(), ansiReset)
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(func(a slog.Attr) bool { write(a); return true })
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *consoleHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return &c
}

func (h *consoleHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.groups = append(append([]string{}, h.groups...), name)
	return &c
}
