package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/store"
	"golang.org/x/term"
)

// The plain CLI is the interface that works everywhere: no raw mode, no
// alt-screen, no cursor addressing. It is what you reach for over a flaky SSH
// link, inside a pipeline, or when you want to see exactly what the agent is
// doing without a full-screen frame redrawing over it.

// chatOptions are the flags the chat command accepts.
type chatOptions struct {
	message     string
	sessionID   string
	continueRun bool
	interactive bool
	quiet       bool
	jsonOut     bool
	model       string
	toolset     string
	role        string
}

func parseChatArgs(args []string) (chatOptions, error) {
	var o chatOptions
	var words []string

	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-i", "--interactive":
			o.interactive = true
		case "-q", "--quiet":
			o.quiet = true
		case "--json":
			o.jsonOut = true
		case "-c", "--continue":
			o.continueRun = true
		case "-s", "--session":
			if i+1 >= len(args) {
				return o, errors.New("--session needs an id")
			}
			i++
			o.sessionID = args[i]
		case "-m", "--model":
			if i+1 >= len(args) {
				return o, errors.New("--model needs a model id")
			}
			i++
			o.model = args[i]
		case "-t", "--toolset":
			if i+1 >= len(args) {
				return o, errors.New("--toolset needs a name")
			}
			i++
			o.toolset = args[i]
		case "-r", "--role":
			if i+1 >= len(args) {
				return o, errors.New("--role needs a name")
			}
			i++
			o.role = args[i]
		default:
			if strings.HasPrefix(a, "-") && len(a) > 1 {
				return o, fmt.Errorf("unknown flag %q — see `antares help`", a)
			}
			words = append(words, a)
		}
	}
	o.message = strings.TrimSpace(strings.Join(words, " "))
	return o, nil
}

func cmdChat(args []string) error {
	opts, err := parseChatArgs(args)
	if err != nil {
		return err
	}

	// A piped prompt is how this gets used from a script.
	if opts.message == "" && !opts.interactive {
		if stat, err := os.Stdin.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
			piped, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
			if err != nil {
				return err
			}
			opts.message = strings.TrimSpace(string(piped))
		}
	}
	// No message and a terminal in front of us means they want the REPL.
	if opts.message == "" && !opts.interactive {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			opts.interactive = true
		} else {
			return errors.New("usage: antares chat <message>, or `antares chat -i` for a conversation")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	// Continuing means the most recent session, which is almost always what
	// "carry on from before" means.
	if opts.continueRun && opts.sessionID == "" {
		if id := rt.latestSessionID(ctx); id != "" {
			opts.sessionID = id
		}
	}

	if opts.interactive {
		return rt.repl(ctx, &opts)
	}
	_, err = rt.runOnce(ctx, &opts, opts.message)
	return err
}

// latestSessionID finds the most recently updated conversation.
func (rt *runtimeServices) latestSessionID(ctx context.Context) string {
	list, _, err := rt.db.ListSessions(ctx, storeSessionFilter(1))
	if err != nil || len(list) == 0 {
		return ""
	}
	return list[0].ID
}

// runOnce sends one message and prints the reply. It returns the session the
// turn belongs to so a REPL can stay in it.
func (rt *runtimeServices) runOnce(ctx context.Context, opts *chatOptions, message string) (string, error) {
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	// Colour only when a person is looking at it; a pipe gets plain text.
	colour := term.IsTerminal(int(os.Stdout.Fd()))
	dim := ansi(colour, "\x1b[2m")
	red := ansi(colour, "\x1b[31m")
	reset := ansi(colour, "\x1b[0m")

	sessionID := opts.sessionID
	var reply strings.Builder
	atLineStart := true

	_, err := rt.agent.Run(ctx, agent.Request{
		SessionID: opts.sessionID,
		Message:   message,
		Platform:  "cli",
		Model:     opts.model,
		Toolset:   opts.toolset,
		Role:      opts.role,
	}, func(e agent.Event) error {
		if opts.jsonOut {
			raw, err := json.Marshal(e)
			if err != nil {
				return nil
			}
			fmt.Fprintln(out, string(raw))
			return out.Flush()
		}

		switch e.Type {
		case agent.EventSession:
			if e.ID != "" {
				sessionID = e.ID
			}

		case agent.EventText:
			reply.WriteString(e.Delta)
			if !opts.quiet {
				fmt.Fprint(out, e.Delta)
				atLineStart = strings.HasSuffix(e.Delta, "\n")
				_ = out.Flush()
			}

		case agent.EventToolCall:
			if opts.quiet {
				return nil
			}
			if !atLineStart {
				fmt.Fprintln(out)
				atLineStart = true
			}
			fmt.Fprintf(out, "%s· %s %s%s\n", dim, e.Name, summariseArgs(e.Arguments), reset)
			_ = out.Flush()

		case agent.EventToolResult:
			if opts.quiet || !e.IsError {
				return nil
			}
			fmt.Fprintf(out, "%s  failed: %s%s\n", red, firstLineOf(e.Content), reset)
			_ = out.Flush()

		case agent.EventNotice:
			if opts.quiet {
				return nil
			}
			if !atLineStart {
				fmt.Fprintln(out)
				atLineStart = true
			}
			fmt.Fprintf(out, "%s· %s%s\n", dim, e.Message, reset)
			_ = out.Flush()

		case agent.EventApproval:
			// Nothing can be approved from a one-shot run; the request times
			// out on its own. Say so rather than appearing to hang.
			fmt.Fprintf(out, "%s· %s needs approval — answer it in the dashboard%s\n", dim, e.Name, reset)
			_ = out.Flush()

		case agent.EventError:
			fmt.Fprintf(out, "%s%s%s\n", red, e.Err, reset)
			_ = out.Flush()
		}
		return nil
	})
	if err != nil {
		return sessionID, err
	}

	if opts.quiet {
		fmt.Fprintln(out, strings.TrimSpace(reply.String()))
	} else if !atLineStart {
		fmt.Fprintln(out)
	}
	return sessionID, nil
}

// repl is a line-based conversation. Deliberately plain: it works over any
// terminal, scrolls back with the shell's own buffer, and can be piped.
func (rt *runtimeServices) repl(ctx context.Context, opts *chatOptions) error {
	colour := term.IsTerminal(int(os.Stdout.Fd()))
	dim := ansi(colour, "\x1b[2m")
	bold := ansi(colour, "\x1b[1m")
	reset := ansi(colour, "\x1b[0m")

	cfg := rt.cfg
	fmt.Printf("%sAntares%s — %s on %s\n", bold, reset, orDash(cfg.Model.Default), orDash(cfg.Model.Provider))
	fmt.Printf("%sType a message. /help for commands, /quit to leave.%s\n\n", dim, reset)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 4<<20)

	for {
		fmt.Printf("%s>%s ", bold, reset)
		if !in.Scan() {
			fmt.Println()
			return in.Err()
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil
		}

		if name, args, ok := commands.Parse(line); ok {
			quit, err := rt.replCommand(ctx, opts, name, args)
			if err != nil {
				fmt.Printf("%s%v%s\n\n", ansi(colour, "\x1b[31m"), err, reset)
				continue
			}
			if quit {
				return nil
			}
			continue
		}

		id, err := rt.runOnce(ctx, opts, line)
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Printf("%s%v%s\n", ansi(colour, "\x1b[31m"), err, reset)
		}
		if id != "" {
			opts.sessionID = id
		}
		fmt.Println()
	}
}

// replCommand runs a slash command inside the REPL. It reports whether to quit.
func (rt *runtimeServices) replCommand(ctx context.Context, opts *chatOptions, name, args string) (bool, error) {
	res, err := commands.Run(ctx, rt.commandDeps(), commands.Input{
		Name:      name,
		Args:      args,
		SessionID: opts.sessionID,
		Surface:   commands.SurfaceTUI,
	})
	if err != nil {
		return false, err
	}

	switch res.Action.Kind {
	case "quit":
		return true, nil
	case "new", "clear":
		opts.sessionID = ""
		fmt.Print("Started a new session.\n\n")
		return false, nil
	case "resume":
		opts.sessionID = res.Action.Value
	case "stop":
		if opts.sessionID != "" {
			rt.agent.Interrupt(opts.sessionID)
		}
	}

	if res.Output != "" {
		fmt.Println(renderMarkdownPlain(res.Output))
	}
	fmt.Println()
	return false, nil
}

// renderMarkdownPlain strips the little markup the command output uses. A
// terminal that cannot render bold should not show asterisks instead.
func renderMarkdownPlain(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSuffix(strings.TrimPrefix(line, "**"), "**")
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "`", "")
		line = strings.ReplaceAll(line, "_(", "(")
		line = strings.ReplaceAll(line, ")_", ")")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// summariseArgs turns tool arguments into something worth one line.
func summariseArgs(raw string) string {
	var m map[string]any
	if raw == "" || json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	// The fields worth showing, in the order a reader wants them.
	for _, key := range []string{"path", "command", "query", "url", "pattern", "action", "name"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return firstLineOf(truncateArg(s, 72))
			}
		}
	}
	return ""
}

func truncateArg(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// ansi returns the escape only when the output is a terminal.
func ansi(enabled bool, code string) string {
	if enabled {
		return code
	}
	return ""
}

// storeSessionFilter is a small helper so the CLI does not import the store
// package just to name a limit.
func storeSessionFilter(limit int) store.SessionFilter {
	return store.SessionFilter{Limit: limit}
}
