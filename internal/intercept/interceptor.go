package intercept

import (
	"context"
	"sort"
	"sync"
)

// An Interceptor is a way to route some client (a browser, a terminal, an
// Android device, a container…) through the proxy and make it trust the CA.
// This unifies HTTP Toolkit's per-interceptor patterns into one contract so the
// tool/API/dashboard drive them all the same way, and so anything that needs an
// external tool (adb, certutil, java, docker) degrades to a clear "install X"
// message via Available() instead of failing hard.
type Interceptor interface {
	// ID is the stable identifier, e.g. "fresh-chrome", "terminal", "android".
	ID() string
	// Label is a short human name for the picker.
	Label() string
	// Category groups interceptors in the UI: browser, terminal, mobile, etc.
	Category() string
	// Available reports whether this interceptor can run here right now. It
	// never errors hard: ok=false with a reason (a copy-pasteable install hint)
	// is the normal "dependency missing" path.
	Available(ctx context.Context) (ok bool, reason string)
	// Activate starts interception and returns a Session to stop it.
	Activate(ctx context.Context, opts ActivateOpts) (Session, error)
}

// ActivateOpts carries everything an interceptor needs to hook a client to the
// running proxy. The tool layer fills these from the shared Proxy + CA.
type ActivateOpts struct {
	ProxyAddr       string         // host:port the proxy listens on
	CACertPath      string         // on-disk CA PEM path
	SPKIFingerprint string         // base64 SPKI hash for Chromium CT-bypass
	Extra           map[string]any // interceptor-specific (url, container id, …)
}

// Session is a live interception a caller can inspect and stop.
type Session interface {
	ID() string
	Interceptor() string
	Info() map[string]any
	Stop() error
}

// Registry holds the process's interceptors, keyed by id.
type Registry struct {
	mu           sync.RWMutex
	interceptors map[string]Interceptor
	sessions     map[string]Session
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{interceptors: map[string]Interceptor{}, sessions: map[string]Session{}}
}

// Register adds an interceptor.
func (r *Registry) Register(i Interceptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.interceptors[i.ID()] = i
}

// Get looks up an interceptor by id.
func (r *Registry) Get(id string) (Interceptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.interceptors[id]
	return i, ok
}

// List returns every interceptor, sorted by category then id.
func (r *Registry) List() []Interceptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Interceptor, 0, len(r.interceptors))
	for _, i := range r.interceptors {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Category() != out[b].Category() {
			return out[a].Category() < out[b].Category()
		}
		return out[a].ID() < out[b].ID()
	})
	return out
}

// PutSession records a live session so it can be listed and stopped later.
func (r *Registry) PutSession(s Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID()] = s
}

// Sessions returns the live sessions.
func (r *Registry) Sessions() []Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID() < out[b].ID() })
	return out
}

// StopSession stops and forgets one session.
func (r *Registry) StopSession(id string) error {
	r.mu.Lock()
	s := r.sessions[id]
	delete(r.sessions, id)
	r.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.Stop()
}
