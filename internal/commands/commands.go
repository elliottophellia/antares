// Package commands is the one place slash commands are defined. The terminal
// UI, the web chat, and the messaging gateways all dispatch through it, so a
// command behaves the same wherever it is typed and only has to be written once.
package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/mcp"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
)

// Surface is where a command was typed. Some commands only make sense in one
// of them — /quit means nothing in a browser tab.
type Surface string

const (
	SurfaceTUI     Surface = "tui"
	SurfaceWeb     Surface = "web"
	SurfaceGateway Surface = "gateway"
)

// Spec describes a command for palettes and help output.
type Spec struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Args is a usage hint like "<id>", empty when the command takes none.
	Args string `json:"args,omitempty"`
	// Surfaces lists where the command is offered.
	Surfaces []Surface `json:"surfaces"`
	// Client marks a command the calling surface must carry out itself —
	// clearing its own transcript, quitting, opening a page. The server has no
	// way to do those, so it only reports the intent.
	Client bool `json:"client,omitempty"`
}

// Action is a machine-readable instruction for the calling surface, returned
// alongside the human-readable output.
type Action struct {
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value,omitempty"`
}

// Result is what running a command produced.
type Result struct {
	// Output is markdown to show in the transcript.
	Output string `json:"output"`
	Action Action `json:"action,omitempty"`
}

// Deps are the services commands read and write. Any of them may be nil; a
// command that needs a missing one says so rather than panicking.
type Deps struct {
	Config  func() *config.Config
	Agent   *agent.Agent
	Store   store.Store
	Skills  *skills.Manager
	MCP     *mcp.Manager
	Reload  func() error
	Version string
	// WebURL is the dashboard address, shown by /web.
	WebURL string
}

type handler func(ctx context.Context, d Deps, in Input) (Result, error)

// Input is one invocation.
type Input struct {
	// Name is the command without its leading slash.
	Name string
	// Args is everything after the command name, trimmed.
	Args string
	// SessionID is the conversation the command was typed in, when there is one.
	SessionID string
	Surface   Surface
}

type entry struct {
	Spec
	run handler
}

var all = map[string]entry{}

func register(s Spec, h handler) {
	all[s.Name] = entry{Spec: s, run: h}
}

// every surface, for the many commands that work anywhere.
var anywhere = []Surface{SurfaceTUI, SurfaceWeb, SurfaceGateway}

func init() {
	register(Spec{Name: "help", Summary: "Show every command", Surfaces: anywhere}, cmdHelp)
	register(Spec{Name: "status", Summary: "Runtime and storage summary", Surfaces: anywhere}, cmdStatus)
	register(Spec{Name: "version", Summary: "Show the version", Surfaces: anywhere}, cmdVersion)

	register(Spec{Name: "model", Args: "[id]", Summary: "Show or change the active model", Surfaces: anywhere}, cmdModel)
	register(Spec{Name: "models", Args: "[provider]", Summary: "List models the provider offers", Surfaces: anywhere}, cmdModels)
	register(Spec{Name: "provider", Args: "[id]", Summary: "Show or change the provider", Surfaces: anywhere}, cmdProvider)

	register(Spec{Name: "tools", Summary: "List the tools available this turn", Surfaces: anywhere}, cmdTools)
	register(Spec{Name: "toolset", Args: "[name]", Summary: "Switch the active toolset", Surfaces: anywhere}, cmdToolset)
	register(Spec{Name: "skills", Args: "[search|install] [id]", Summary: "List, search, or install skills", Surfaces: anywhere}, cmdSkills)
	register(Spec{Name: "memory", Args: "[query]", Summary: "Search or list long-term memory", Surfaces: anywhere}, cmdMemory)
	register(Spec{Name: "remember", Args: "<text>", Summary: "Save something to long-term memory", Surfaces: anywhere}, cmdRemember)
	register(Spec{Name: "forget", Args: "<key>", Summary: "Delete a memory by key", Surfaces: anywhere}, cmdForget)
	register(Spec{Name: "mcp", Args: "[search|install] [id]", Summary: "Show, search, or install MCP servers", Surfaces: anywhere}, cmdMCP)
	register(Spec{Name: "config", Args: "[path] [value]", Summary: "Read or set a config value", Surfaces: anywhere}, cmdConfig)
	register(Spec{Name: "sessions", Summary: "List recent sessions", Surfaces: anywhere}, cmdSessions)
	register(Spec{Name: "usage", Args: "[days]", Summary: "Token and cost summary", Surfaces: anywhere}, cmdUsage)
	register(Spec{Name: "cost", Args: "[days]", Summary: "Alias for /usage", Surfaces: anywhere}, cmdUsage)

	// Client-side: the server can only name the intent.
	register(Spec{Name: "new", Summary: "Start a fresh session", Surfaces: anywhere, Client: true}, clientAction("new"))
	register(Spec{Name: "clear", Summary: "Clear the transcript", Surfaces: anywhere, Client: true}, clientAction("clear"))
	register(Spec{Name: "stop", Summary: "Interrupt the current turn", Surfaces: anywhere, Client: true}, cmdStop)
	register(Spec{Name: "retry", Summary: "Resend the last message", Surfaces: anywhere, Client: true}, clientAction("retry"))
	register(Spec{Name: "resume", Args: "<id>", Summary: "Resume a session by id", Surfaces: []Surface{SurfaceTUI, SurfaceWeb}, Client: true}, cmdResume)
	register(Spec{Name: "compact", Summary: "Summarise this session now", Surfaces: anywhere}, cmdCompact)
	register(Spec{Name: "copy", Summary: "Copy the last reply to the clipboard", Surfaces: []Surface{SurfaceTUI, SurfaceWeb}, Client: true}, clientAction("copy"))
	register(Spec{Name: "web", Summary: "Print the dashboard URL", Surfaces: anywhere}, cmdWeb)
	register(Spec{Name: "quit", Summary: "Leave the TUI", Surfaces: []Surface{SurfaceTUI}, Client: true}, clientAction("quit"))
	register(Spec{Name: "setup", Summary: "Open the setup wizard", Surfaces: []Surface{SurfaceTUI, SurfaceWeb}, Client: true}, clientAction("setup")) //nolint:misspell
	register(Spec{Name: "reasoning", Args: "[on|off]", Summary: "Toggle reasoning display", Surfaces: anywhere}, cmdReasoning)
}

// Catalogue returns the commands offered on a surface, sorted by name. Passing
// an empty surface returns everything.
func Catalogue(s Surface) []Spec {
	out := make([]Spec, 0, len(all))
	for _, e := range all {
		if s == "" || containsSurface(e.Surfaces, s) {
			out = append(out, e.Spec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup finds one command.
func Lookup(name string) (Spec, bool) {
	e, ok := all[strings.ToLower(strings.TrimSpace(name))]
	return e.Spec, ok
}

// Parse splits a typed line into a command name and its arguments. It reports
// false when the line is not a command at all, so callers can pass ordinary
// messages straight through.
func Parse(line string) (name, args string, ok bool) {
	trimmed := strings.TrimSpace(line)
	// A bare "/" or a path like "/etc/hosts" is not a command.
	if !strings.HasPrefix(trimmed, "/") || len(trimmed) < 2 {
		return "", "", false
	}
	name, args, _ = strings.Cut(trimmed[1:], " ")
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(args), true
}

// Run executes a command. An unknown name is an error the caller shows as-is.
func Run(ctx context.Context, d Deps, in Input) (Result, error) {
	e, ok := all[strings.ToLower(in.Name)]
	if !ok {
		return Result{}, fmt.Errorf("unknown command /%s — try /help", in.Name)
	}
	if in.Surface != "" && !containsSurface(e.Surfaces, in.Surface) {
		return Result{}, fmt.Errorf("/%s does not apply here", in.Name)
	}
	return e.run(ctx, d, in)
}

func containsSurface(list []Surface, s Surface) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// clientAction builds a handler for a command the surface carries out itself.
func clientAction(kind string) handler {
	return func(_ context.Context, _ Deps, in Input) (Result, error) {
		return Result{Action: Action{Kind: kind, Value: in.Args}}, nil
	}
}
