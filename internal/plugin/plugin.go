// Package plugin runs external programs at points in the agent's life, so
// behaviour can be added without rebuilding Antares.
//
// MCP already covers adding tools, and does it better than a bespoke protocol
// would. What it does not cover is watching and gating what the agent does:
// refusing a tool call by policy, rewriting a result, logging every turn to
// something of your own. That is what a hook is for.
//
// A plugin is a directory with a manifest and an executable. The executable is
// handed one JSON event on stdin and answers with one JSON object on stdout.
// Any language that can read a pipe can write one.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Event names the moment a plugin is being called for.
type Event string

const (
	// PreToolCall runs before a tool. The reply may deny it or change its
	// arguments.
	PreToolCall Event = "pre_tool_call"
	// PostToolCall runs after one. The reply may replace the result the model
	// sees.
	PostToolCall Event = "post_tool_call"
	// SessionStart and SessionEnd bracket a conversation.
	SessionStart Event = "session_start"
	SessionEnd   Event = "session_end"
	// TurnEnd runs after each completed turn.
	TurnEnd Event = "turn_end"
)

// AllEvents is every hook a manifest may name.
var AllEvents = []Event{PreToolCall, PostToolCall, SessionStart, SessionEnd, TurnEnd}

// Manifest is a plugin's plugin.yaml.
type Manifest struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Version     string `yaml:"version" json:"version"`
	// Command is the executable, relative to the plugin directory unless
	// absolute or on PATH.
	Command string   `yaml:"command" json:"command"`
	Args    []string `yaml:"args" json:"args"`
	// Hooks are the events this plugin wants. An empty list means none, which
	// makes the plugin inert rather than universal.
	Hooks []Event `yaml:"hooks" json:"hooks"`
	// TimeoutMS bounds one call. A plugin that hangs must not hang the agent.
	TimeoutMS int               `yaml:"timeout_ms" json:"timeout_ms"`
	Env       map[string]string `yaml:"env" json:"env"`

	// Filled in at load time.
	Dir     string `yaml:"-" json:"dir"`
	Enabled bool   `yaml:"-" json:"enabled"`
	Error   string `yaml:"-" json:"error,omitempty"`
}

// Payload is what a plugin is handed.
type Payload struct {
	Event     Event  `json:"event"`
	SessionID string `json:"session_id,omitempty"`
	Platform  string `json:"platform,omitempty"`

	// Tool events.
	Tool      string `json:"tool,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// Turn events.
	Turn  int    `json:"turn,omitempty"`
	Reply string `json:"reply,omitempty"`
}

// Reply is what a plugin may answer with. Every field is optional; an empty
// object means "no opinion", which is the common case.
type Reply struct {
	// Deny refuses a tool call. Reason is shown to the model.
	Deny   bool   `json:"deny,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Arguments replaces the call's arguments when non-empty.
	Arguments string `json:"arguments,omitempty"`
	// Result replaces what the model sees from a finished tool.
	Result string `json:"result,omitempty"`
	// Notice is surfaced in the transcript.
	Notice string `json:"notice,omitempty"`
}

// Manager loads plugins and dispatches events to them.
type Manager struct {
	mu      sync.RWMutex
	plugins []Manifest
	dirs    []string
}

// NewManager builds a manager over the given directories.
func NewManager(dirs []string) *Manager {
	return &Manager{dirs: dirs}
}

// Load rescans every directory. A plugin that cannot be loaded is kept in the
// list with its error, so the dashboard can say what is wrong rather than the
// plugin silently not existing.
func (m *Manager) Load() error {
	var found []Manifest

	for _, dir := range m.dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			man, err := loadManifest(path)
			if err != nil {
				found = append(found, Manifest{
					Name: e.Name(), Dir: path, Error: err.Error(),
				})
				continue
			}
			found = append(found, man)
		}
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	m.mu.Lock()
	m.plugins = found
	m.mu.Unlock()
	return nil
}

func loadManifest(dir string) (Manifest, error) {
	var man Manifest
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return man, errors.New("no plugin.yaml")
	}
	if err := yaml.Unmarshal(raw, &man); err != nil {
		return man, fmt.Errorf("plugin.yaml is not valid YAML: %w", err)
	}
	if strings.TrimSpace(man.Name) == "" {
		man.Name = filepath.Base(dir)
	}
	if strings.TrimSpace(man.Command) == "" {
		return man, errors.New("plugin.yaml has no command")
	}
	for _, h := range man.Hooks {
		if !validEvent(h) {
			return man, fmt.Errorf("unknown hook %q", h)
		}
	}
	man.Dir = dir
	man.Enabled = true
	return man, nil
}

func validEvent(e Event) bool {
	for _, known := range AllEvents {
		if known == e {
			return true
		}
	}
	return false
}

// List reports what is loaded.
func (m *Manager) List() []Manifest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Manifest, len(m.plugins))
	copy(out, m.plugins)
	return out
}

// Count is how many loaded without error.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, p := range m.plugins {
		if p.Error == "" && p.Enabled {
			n++
		}
	}
	return n
}

// SetEnabled turns one plugin on or off for this process.
func (m *Manager) SetEnabled(name string, enabled bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.plugins {
		if m.plugins[i].Name == name {
			m.plugins[i].Enabled = enabled
			return true
		}
	}
	return false
}

// Dispatch sends an event to every plugin that asked for it and folds the
// replies together.
//
// Plugins run in order, each seeing the previous one's changes. A deny from
// any of them ends it: a refusal is not something a later plugin should be
// able to quietly undo.
func (m *Manager) Dispatch(ctx context.Context, p Payload) Reply {
	m.mu.RLock()
	plugins := make([]Manifest, len(m.plugins))
	copy(plugins, m.plugins)
	m.mu.RUnlock()

	var folded Reply
	for _, man := range plugins {
		if man.Error != "" || !man.Enabled || !wants(man, p.Event) {
			continue
		}
		reply, err := call(ctx, man, p)
		if err != nil {
			// A broken plugin must not break the agent. Log it and carry on.
			slog.Warn("plugin failed", "plugin", man.Name, "event", p.Event, "error", err)
			continue
		}
		if reply.Deny {
			reply.Reason = strings.TrimSpace(reply.Reason)
			if reply.Reason == "" {
				reply.Reason = man.Name + " refused this"
			}
			return reply
		}
		if reply.Arguments != "" {
			folded.Arguments = reply.Arguments
			p.Arguments = reply.Arguments
		}
		if reply.Result != "" {
			folded.Result = reply.Result
			p.Result = reply.Result
		}
		if reply.Notice != "" {
			if folded.Notice != "" {
				folded.Notice += "; "
			}
			folded.Notice += reply.Notice
		}
	}
	return folded
}

func wants(man Manifest, e Event) bool {
	for _, h := range man.Hooks {
		if h == e {
			return true
		}
	}
	return false
}

// call runs one plugin once.
func call(ctx context.Context, man Manifest, p Payload) (Reply, error) {
	var reply Reply

	timeout := time.Duration(man.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := man.Command
	// A bare name is looked up on PATH; anything with a separator is relative
	// to the plugin's own directory, which is how a bundled script is found.
	if strings.ContainsRune(command, os.PathSeparator) && !filepath.IsAbs(command) {
		command = filepath.Join(man.Dir, command)
	}

	payload, err := json.Marshal(p)
	if err != nil {
		return reply, err
	}

	cmd := exec.CommandContext(runCtx, command, man.Args...)
	cmd.Dir = man.Dir
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(os.Environ(), "ANTARES_PLUGIN="+man.Name)
	for k, v := range man.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return reply, fmt.Errorf("timed out after %s", timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return reply, errors.New(msg)
	}

	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		// Silence is a valid answer: the plugin observed and had no opinion.
		return reply, nil
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return reply, fmt.Errorf("returned something that is not JSON: %s", truncate(string(out), 200))
	}
	return reply, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
