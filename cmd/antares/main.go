// Command antares runs the Antares agent: HTTP API, dashboard, and CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cron"
	"github.com/enowdev/antares/internal/gateway"
	"github.com/enowdev/antares/internal/logx"
	"github.com/enowdev/antares/internal/mcp"
	"github.com/enowdev/antares/internal/rag"
	"github.com/enowdev/antares/internal/server"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
	"github.com/enowdev/antares/internal/tui"
	"github.com/enowdev/antares/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "antares: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]

	// Bare `antares` opens the TUI when there is a terminal to draw on, and
	// falls back to serving when there is not (systemd, Docker, cron).
	command := "tui"
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		command = "serve"
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}

	switch command {
	case "serve", "start":
		return cmdServe()
	case "tui", "chat-ui":
		return cmdTUI()
	case "chat":
		return cmdChat(args)
	case "config":
		return cmdConfig(args)
	case "model":
		return cmdModel(args)
	case "setup":
		return cmdSetup(args)
	case "doctor":
		return cmdDoctor()
	case "version", "--version", "-v":
		fmt.Printf("%s %s (commit %s, built %s)\n", version.Display, version.Version, version.Commit, version.Date)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", command)
	}
}

func printUsage() {
	fmt.Printf(`%s %s — AI agent

Usage:
  antares                  Open the terminal UI (serves the API when headless)
  antares serve            Run the API server and dashboard
  antares tui              Open the terminal UI explicitly
  antares setup            Configure Antares (web or terminal wizard)
  antares chat <message>   Send one message and print the reply
  antares model [id]       Show or change the active model
  antares config get <path>
  antares config set <path> <value>
  antares doctor           Check configuration and connectivity
  antares version

Environment:
  ANTARES_HOME             State directory (default ~/.antares)
  ANTARES_CONFIG           Configuration file
  ANTARES_PORT             HTTP port
  ANTARES_MODEL            Model override
  ANTARES_LOG_LEVEL        debug|info|warn|error
`, version.Display, version.Version)
}

func cmdTUI() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	// A first run with nothing configured drops into setup rather than an
	// empty prompt the user cannot do anything with.
	if needsSetup(rt.cfg) {
		if err := runSetupWizard(ctx, rt); err != nil {
			return err
		}
		cfg, err := config.Reload()
		if err != nil {
			return err
		}
		rt.cfg = cfg
		rt.agent.SetConfig(cfg)
	}

	return tui.New(rt.agent, rt.cfg, rt.db).Run(ctx)
}

// runtimeServices bundles everything a running server needs, so a config reload
// can rebuild the pieces that depend on configuration.
type runtimeServices struct {
	mu      sync.Mutex
	cfg     *config.Config
	db      store.Store
	shell   *tools.ShellManager
	agent   *agent.Agent
	skills  *skills.Manager
	cron    *cron.Runner
	gateway *gateway.Manager
	mcp     *mcp.Manager
}

func bootstrap(ctx context.Context) (*runtimeServices, error) {
	if err := config.EnsureHome(); err != nil {
		return nil, fmt.Errorf("preparing %s: %w", config.Home(), err)
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := logx.Setup(cfg.Logging.Level, cfg.Logging.File, cfg.Logging.JSON); err != nil {
		return nil, fmt.Errorf("setting up logging: %w", err)
	}
	if err := os.MkdirAll(cfg.Agent.Workspace, 0o755); err != nil {
		return nil, fmt.Errorf("preparing workspace: %w", err)
	}

	db, err := store.Open(ctx, cfg.Database.Driver, cfg.Database.DSN,
		cfg.Database.MaxConns, cfg.Database.Busy, cfg.Database.WAL)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	shell := tools.NewShellManager(cfg.Terminal)
	ragProvider, err := rag.New(cfg, db)
	if err != nil {
		slog.Warn("RAG disabled", "error", err)
		ragProvider = nil
	}

	skillMgr := skills.NewManager(cfg.Skills.Dirs)
	if err := skillMgr.Reload(); err != nil {
		slog.Warn("some skills failed to load", "error", err)
	}

	ag := agent.New(cfg, db, tools.Default(), shell, ragProvider)
	ag.SetSkills(skillMgr)

	rt := &runtimeServices{cfg: cfg, db: db, shell: shell, agent: ag, skills: skillMgr}

	// MCP servers are optional; a failing one is recorded, never fatal.
	rt.mcp = mcp.NewManager()
	rt.mcp.Connect(ctx, cfg)
	if names := rt.mcp.Register(tools.Default()); len(names) > 0 {
		slog.Info("mcp tools registered", "count", len(names))
	}

	rt.gateway = gateway.NewManager(cfg, db, rt.handleGatewayMessage)
	rt.cron = cron.New(cron.Options{
		Store:         db,
		Execute:       rt.runCronJob,
		Deliver:       rt.gateway.Deliver,
		Timezone:      cfg.Cron.Timezone,
		MaxConcurrent: cfg.Cron.MaxConcurrent,
		HistoryLimit:  cfg.Cron.HistoryLimit,
	})
	return rt, nil
}

// handleGatewayMessage runs one platform message through the agent, reusing a
// persistent session per channel so conversations stay continuous.
func (rt *runtimeServices) handleGatewayMessage(ctx context.Context, msg gateway.InboundMessage, partial func(string)) (string, error) {
	key := "gateway_session:" + msg.Platform + ":" + msg.ChannelID
	sessionID, err := rt.db.GetKV(ctx, key)
	if err != nil {
		sessionID = ""
	}

	var reply strings.Builder
	res, err := rt.agent.Run(ctx, agent.Request{
		SessionID: sessionID,
		Message:   msg.Text,
		Platform:  msg.Platform,
		UserID:    msg.UserID,
		ChannelID: msg.ChannelID,
	}, func(e agent.Event) error {
		switch e.Type {
		case agent.EventSession:
			if e.ID != "" && e.ID != sessionID {
				sessionID = e.ID
				_ = rt.db.SetKV(ctx, key, e.ID)
			}
		case agent.EventText:
			reply.WriteString(e.Delta)
			if partial != nil {
				partial(reply.String())
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if res != nil && res.Reply != "" {
		return res.Reply, nil
	}
	return reply.String(), nil
}

// runCronJob executes a scheduled prompt in its own throwaway session.
func (rt *runtimeServices) runCronJob(ctx context.Context, job store.CronJob) (string, string, error) {
	var reply strings.Builder
	sessionID := ""
	res, err := rt.agent.Run(ctx, agent.Request{
		Message:     job.Prompt,
		Platform:    "cron",
		SystemExtra: "You are running unattended on a schedule named " + job.Name + ". Nobody can answer follow-up questions, so make reasonable assumptions and finish the task.",
	}, func(e agent.Event) error {
		switch e.Type {
		case agent.EventSession:
			sessionID = e.ID
		case agent.EventText:
			reply.WriteString(e.Delta)
		}
		return nil
	})
	if err != nil {
		return sessionID, "", err
	}
	if res != nil && res.Reply != "" {
		return sessionID, res.Reply, nil
	}
	return sessionID, reply.String(), nil
}

// reload re-reads config and rebuilds config-dependent services in place.
func (rt *runtimeServices) reload() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	cfg, err := config.Reload()
	if err != nil {
		return err
	}
	rt.cfg = cfg
	rt.agent.SetConfig(cfg)

	ragProvider, err := rag.New(cfg, rt.db)
	if err != nil {
		slog.Warn("RAG disabled after reload", "error", err)
		ragProvider = nil
	}
	rt.agent.SetRAG(ragProvider)

	rt.skills = skills.NewManager(cfg.Skills.Dirs)
	if err := rt.skills.Reload(); err != nil {
		slog.Warn("some skills failed to load", "error", err)
	}
	rt.agent.SetSkills(rt.skills)

	// The gateway holds its own pointer; without this it would keep reconciling
	// against the configuration it was constructed with.
	if rt.gateway != nil {
		rt.gateway.SetConfig(cfg)
	}

	slog.Info("configuration reloaded", "model", cfg.Model.Default, "provider", cfg.Model.Provider)
	return nil
}

func (rt *runtimeServices) close() {
	if rt.mcp != nil {
		rt.mcp.Close()
	}
	if rt.gateway != nil {
		rt.gateway.StopAll()
	}
	rt.shell.CloseAll()
	_ = rt.db.Close()
}

func cmdServe() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	srv := server.New(server.Options{
		Config:  rt.cfg,
		Agent:   rt.agent,
		Store:   rt.db,
		Dist:    server.EmbeddedDist(),
		Reload:  rt.reload,
		Skills:  rt.skills,
		Cron:    rt.cron,
		Gateway: rt.gateway,
		MCP:     rt.mcp,
	})

	if rt.cfg.Cron.Enabled {
		go rt.cron.Start(ctx)
	}
	rt.gateway.Start(ctx)

	// Reap idle shells so long-running servers do not leak processes.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rt.shell.ReapIdle(time.Duration(rt.cfg.Terminal.LifetimeSeconds) * time.Second)
			}
		}
	}()

	printBanner(rt.cfg)
	if err := srv.Serve(ctx); err != nil {
		return err
	}
	slog.Info("antares stopped")
	return nil
}

func printBanner(cfg *config.Config) {
	fmt.Printf("\n  %s %s\n", version.Display, version.Version)
	fmt.Printf("  ├ API      http://%s:%d\n", displayHost(cfg.Server.Host), cfg.Server.Port)
	fmt.Printf("  ├ Model    %s (%s)\n", orDash(cfg.Model.Default), orDash(cfg.Model.Provider))
	fmt.Printf("  ├ Database %s\n", cfg.Database.Driver)
	fmt.Printf("  ├ Workspace %s\n", cfg.Agent.Workspace)
	if cfg.Server.AuthToken == "" {
		fmt.Printf("  └ Auth     open (set server.auth_token to lock it down)\n\n")
	} else {
		fmt.Printf("  └ Auth     token required\n\n")
	}
}

func displayHost(h string) string {
	if h == "" || h == "0.0.0.0" {
		return "localhost"
	}
	return h
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func cmdChat(args []string) error {
	message := strings.TrimSpace(strings.Join(args, " "))
	if message == "" {
		return errors.New("usage: antares chat <message>")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	_, err = rt.agent.Run(ctx, agent.Request{Message: message, Platform: "cli"}, func(e agent.Event) error {
		switch e.Type {
		case agent.EventText:
			fmt.Print(e.Delta)
		case agent.EventToolCall:
			fmt.Printf("\n  → %s\n", e.Name)
		case agent.EventNotice:
			fmt.Printf("\n  ℹ %s\n", e.Message)
		case agent.EventError:
			fmt.Printf("\n  ✗ %s\n", e.Err)
		case agent.EventDone:
			fmt.Println()
		}
		return nil
	})
	return err
}

func cmdConfig(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: antares config get|set|path <path> [value]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch args[0] {
	case "path":
		fmt.Println(config.ConfigFile())
		return nil
	case "get":
		if len(args) < 2 {
			return errors.New("usage: antares config get <path>")
		}
		v, err := cfg.GetPath(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("%v\n", v)
		return nil
	case "set":
		if len(args) < 3 {
			return errors.New("usage: antares config set <path> <value>")
		}
		if err := cfg.SetPath(args[1], args[2]); err != nil {
			return err
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("%s = %s\n", args[1], args[2])
		return nil
	default:
		return fmt.Errorf("unknown config subcommand: %s", args[0])
	}
}

func cmdModel(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		fmt.Printf("%s (%s)\n", orDash(cfg.Model.Default), orDash(cfg.Model.Provider))
		return nil
	}
	cfg.Model.Default = args[0]
	if len(args) > 1 {
		cfg.Model.Provider = args[1]
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("active model: %s (%s)\n", cfg.Model.Default, cfg.Model.Provider)
	return nil
}

func cmdDoctor() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rt, err := bootstrap(ctx)
	if err != nil {
		fmt.Printf("✗ bootstrap: %v\n", err)
		return err
	}
	defer rt.close()

	fmt.Printf("✓ config        %s\n", config.ConfigFile())
	fmt.Printf("✓ workspace     %s\n", rt.cfg.Agent.Workspace)

	if err := rt.db.Ping(ctx); err != nil {
		fmt.Printf("✗ database      %v\n", err)
	} else {
		st, _ := rt.db.Stats(ctx)
		fmt.Printf("✓ database      %s · %d sessions, %d messages\n", rt.db.Driver(), st.Sessions, st.Messages)
	}

	ok, detail := rt.agent.Probe(ctx)
	if ok {
		fmt.Printf("✓ provider      %s\n", detail)
	} else {
		fmt.Printf("✗ provider      %s\n", detail)
	}

	if p := rt.agent.RAG(); p != nil {
		status := rag.Describe(ctx, rt.cfg, p)
		mark := "✓"
		if !status.Reachable {
			mark = "✗"
		}
		fmt.Printf("%s rag           %s — %s\n", mark, status.Provider, status.Detail)
	} else {
		fmt.Printf("· rag           disabled\n")
	}

	for _, st := range rt.mcp.Status(rt.cfg) {
		if st.Connected {
			fmt.Printf("✓ mcp %-10s %d tool(s)\n", st.Name, len(st.Tools))
		} else {
			fmt.Printf("✗ mcp %-10s %s\n", st.Name, st.Error)
		}
	}
	fmt.Printf("✓ tools         %d registered\n", len(tools.Default().Names()))
	return nil
}
