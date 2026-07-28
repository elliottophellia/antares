// Internal logger for the hackbrowser engine. Bridges to log/slog so the
// crawl's output lands in the same log stream as the rest of antares, but
// keeps the surface the ported TS code uses (Debug/Info/Warn/Error with a
// contextualized service name and optional structured extras).
//
// Two knobs the caller can turn:
//   - Log.Init(level) sets the global threshold (default INFO).
//   - Log.SetSink(fn) replaces the slog bridge with a custom transport. Used
//     by the launcher to forward records into antares' main logger when
//     hackbrowser runs as a sub-component of a tool call.

package hackbrowser

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// LogLevel orders severity. Priority grows top-to-bottom.
type LogLevel string

const (
	LevelDebug LogLevel = "DEBUG"
	LevelInfo  LogLevel = "INFO"
	LevelWarn  LogLevel = "WARN"
	LevelError LogLevel = "ERROR"
)

// LogRecord is one structured log entry.
type LogRecord struct {
	Level     LogLevel         `json:"level"`
	Service   string           `json:"service"`
	Message   string           `json:"message"`
	Extra     map[string]any   `json:"extra,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
}

// LogSink receives records. Replace via SetSink.
type LogSink func(record LogRecord)

// Logger is the per-service handle.
type Logger interface {
	Debug(message string, extra ...LogExtra)
	Info(message string, extra ...LogExtra)
	Warn(message string, extra ...LogExtra)
	Error(message string, extra ...LogExtra)
}

// LogExtra is one key/value pair for structured logging. Construct with
// F("key", value) and pass variadically: log.Info("login detected",
// F("user", name), F("after", elapsed)).
type LogExtra struct {
	Key   string
	Value any
}

// F builds one LogExtra. The single-letter name keeps call sites readable.
func F(key string, value any) LogExtra { return LogExtra{Key: key, Value: value} }

// Log is the package-level logger configuration. Hold the singleton state
// (current level, current sink) and produce per-service Loggers.
var Log = newLogState()

type logState struct {
	mu          sync.Mutex
	current     LogLevel
	sink        LogSink
	stdlog      *slog.Logger
}

func newLogState() *logState {
	st := &logState{
		current: LevelInfo,
		stdlog:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	st.sink = st.defaultSlogSink
	return st
}

// Init sets the global threshold. Records below this level are dropped
// before they reach the sink.
func (s *logState) Init(level LogLevel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = level
	var lvl slog.Level
	switch level {
	case LevelDebug:
		lvl = slog.LevelDebug
	case LevelWarn:
		lvl = slog.LevelWarn
	case LevelError:
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	s.stdlog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// SetSink replaces the transport. Pass nil to restore the default.
func (s *logState) SetSink(sink LogSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sink == nil {
		s.sink = s.defaultSlogSinkLocked
		return
	}
	s.sink = sink
}

// ResetSink restores the default slog bridge. Useful for tests.
func (s *logState) ResetSink() { s.SetSink(nil) }

// Create returns a Logger bound to a service name. The intended pattern is a
// package-level singleton: var log = Log.Create("hackbrowser:agent").
func (s *logState) Create(service string) Logger {
	return &serviceLogger{state: s, service: service}
}

func (s *logState) shouldLog(level LogLevel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return priority(level) >= priority(s.current)
}

func (s *logState) emit(level LogLevel, service, message string, extra []LogExtra) {
	if !s.shouldLog(level) {
		return
	}
	rec := LogRecord{
		Level:     level,
		Service:   service,
		Message:   message,
		Timestamp: time.Now(),
	}
	if len(extra) > 0 {
		rec.Extra = make(map[string]any, len(extra))
		for _, e := range extra {
			rec.Extra[e.Key] = e.Value
		}
	}
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	sink(rec)
}

// defaultSlogSinkLocked forwards a record into antares' slog stream so
// hackbrowser output appears in the same log file as everything else. The
// slog level is mapped from our LogLevel.
func (s *logState) defaultSlogSinkLocked(rec LogRecord) {
	// Caller holds the mutex (or is in a context where it does not need to).
	// Read s.stdlog via a fresh fetch to honor Init changes.
	var lvl slog.Level
	switch rec.Level {
	case LevelDebug:
		lvl = slog.LevelDebug
	case LevelWarn:
		lvl = slog.LevelWarn
	case LevelError:
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	args := make([]any, 0, 2+len(rec.Extra)*2)
	args = append(args, "service", rec.Service)
	for k, v := range rec.Extra {
		args = append(args, k, v)
	}
	s.stdlog.Log(nil, lvl, rec.Message, args...)
}

// defaultSlogSink is the unlocked entry point used as the initial sink.
func (s *logState) defaultSlogSink(rec LogRecord) {
	s.defaultSlogSinkLocked(rec)
}

// serviceLogger implements Logger by delegating to its parent state.
type serviceLogger struct {
	state   *logState
	service string
}

func (l *serviceLogger) Debug(message string, extra ...LogExtra) { l.state.emit(LevelDebug, l.service, message, extra) }
func (l *serviceLogger) Info(message string, extra ...LogExtra)  { l.state.emit(LevelInfo, l.service, message, extra) }
func (l *serviceLogger) Warn(message string, extra ...LogExtra)  { l.state.emit(LevelWarn, l.service, message, extra) }
func (l *serviceLogger) Error(message string, extra ...LogExtra) { l.state.emit(LevelError, l.service, message, extra) }

func priority(l LogLevel) int {
	switch l {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	}
	// Unknown level — treat as info.
	return 1
}

// parseLogLevel maps a case-insensitive string to a LogLevel. Empty string
// returns the default INFO.
func parseLogLevel(s string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return LevelDebug
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	default:
		return LevelInfo
	}
}
