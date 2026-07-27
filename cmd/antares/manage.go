package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/cron"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// cmdCron manages scheduled jobs from the terminal: the same jobs the dashboard
// and the /cron slash command drive.
func cmdCron(args []string) error {
	if len(args) == 0 {
		return cronUsage()
	}
	ctx := context.Background()
	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	switch strings.ToLower(args[0]) {
	case "list", "ls":
		jobs, err := rt.db.ListCronJobs(ctx)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			fmt.Println("No scheduled jobs. Add one with: antares cron add \"name\" \"0 8 * * *\" \"the prompt\"")
			return nil
		}
		for _, j := range jobs {
			state := "enabled"
			if !j.Enabled {
				state = "disabled"
			}
			next := "—"
			if j.NextRun != nil {
				next = j.NextRun.Format("2006-01-02 15:04")
			}
			fmt.Printf("%s  %-20s  %-14s  next %s  [%s]\n", j.ID, truncateCLI(j.Name, 20), j.Schedule, next, state)
			fmt.Printf("    %s\n", truncateCLI(j.Prompt, 100))
		}
		return nil

	case "add", "new":
		if len(args) < 4 {
			return fmt.Errorf("usage: antares cron add \"name\" \"schedule\" \"prompt\"")
		}
		name, schedule := args[1], args[2]
		prompt := strings.Join(args[3:], " ")
		loc := time.Local
		if tz := rt.cfg.Agent.Timezone; tz != "" && tz != "Local" {
			if l, err := time.LoadLocation(tz); err == nil {
				loc = l
			}
		}
		next, err := cron.Validate(schedule, loc)
		if err != nil {
			return fmt.Errorf("invalid schedule %q: %w", schedule, err)
		}
		now := time.Now()
		job := &store.CronJob{
			ID: "cron_" + randHex(6), Name: name, Schedule: schedule, Prompt: prompt,
			Enabled: true, Target: "", Timezone: rt.cfg.Agent.Timezone,
			NextRun: &next, CreatedAt: now, UpdatedAt: now,
		}
		if err := rt.db.PutCronJob(ctx, job); err != nil {
			return err
		}
		fmt.Printf("Added %s — next run %s\n", job.ID, next.Format("2006-01-02 15:04 MST"))
		return nil

	case "rm", "remove", "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: antares cron rm <id>")
		}
		if err := rt.db.DeleteCronJob(ctx, args[1]); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", args[1])
		return nil

	case "run":
		if len(args) < 2 {
			return fmt.Errorf("usage: antares cron run <id>")
		}
		job, err := rt.db.GetCronJob(ctx, args[1])
		if err != nil {
			return fmt.Errorf("no such job %q: %w", args[1], err)
		}
		fmt.Printf("Running %s…\n\n", job.Name)
		_, reply, err := rt.runCronJob(ctx, *job)
		if err != nil {
			return err
		}
		fmt.Println(reply)
		return nil

	default:
		return cronUsage()
	}
}

func cronUsage() error {
	fmt.Println(`Manage scheduled jobs:
  antares cron list
  antares cron add "name" "0 8 * * *" "the prompt to run"
  antares cron run <id>
  antares cron rm <id>`)
	return nil
}

// cmdRag indexes files into the semantic search store from the terminal, by
// driving the same rag_index tool the agent uses.
func cmdRag(args []string) error {
	if len(args) < 2 || strings.ToLower(args[0]) != "index" {
		fmt.Println(`Semantic index:
  antares rag index <path> [--collection name] [--include "**/*.go"]`)
		return nil
	}
	ctx := context.Background()
	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	if rt.agent.RAG() == nil {
		return fmt.Errorf("RAG is disabled — set rag.enabled = true in your config first")
	}

	path := args[1]
	collection, include := "", ""
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--collection", "-c":
			if i+1 < len(args) {
				i++
				collection = args[i]
			}
		case "--include", "-i":
			if i+1 < len(args) {
				i++
				include = args[i]
			}
		}
	}

	tool, ok := rt.agent.Registry().Get("rag_index")
	if !ok {
		return fmt.Errorf("the rag_index tool is not registered")
	}
	cwd, _ := os.Getwd()
	payload, _ := json.Marshal(map[string]string{"path": path, "collection": collection, "include": include})
	res := tool.Execute(ctx, tools.Input{
		Args:      payload,
		Workspace: cwd,
		Emit:      func(p tools.Progress) { fmt.Fprintln(os.Stderr, p.Message) },
		Deps:      &tools.Deps{Config: rt.cfg, RAG: rt.agent.RAG()},
	})
	if res.IsError {
		return fmt.Errorf("%s", res.Content)
	}
	fmt.Println(res.Content)
	return nil
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "000000"
	}
	return hex.EncodeToString(b)
}

func truncateCLI(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
