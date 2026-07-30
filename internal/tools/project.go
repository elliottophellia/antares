package tools

import (
	"context"
	"strings"
)

// projectInfoTool lets the agent record the essential facts about a project
// session — tech stack, key libraries, how to run it — into the session so the
// dashboard's project sidebar can show them. It is a full replace: each call
// overwrites the stored info, so the agent passes the complete picture (it is
// cheap to re-state, and partial merges drift out of sync).
//
// It only applies to project sessions (Meta["project_dir"] set); in an ordinary
// session there is no sidebar to populate, so it refuses rather than storing
// orphan data.
type projectInfoTool struct{}

func (projectInfoTool) Name() string { return "project_info" }

func (projectInfoTool) Description() string {
	return "Record the ESSENTIAL facts about this project for the sidebar: a one-line summary, " +
		"the main languages, frameworks, and key libraries, and how to build and run it. " +
		"Keep it to what matters — a few well-chosen items per list, not an exhaustive dump. " +
		"Each call REPLACES the stored info, so pass the complete picture every time. " +
		"Call this after analyzing a project, and update it when the stack meaningfully changes. " +
		"Only usable in a project session."
}

func (projectInfoTool) Schema() map[string]any {
	return schema(map[string]any{
		"summary":       prop("string", "One or two sentences: what this project is and does."),
		"languages":     arrOf("Primary programming languages, e.g. Go, TypeScript."),
		"frameworks":    arrOf("Main frameworks / runtimes, e.g. React, Gin, Vite."),
		"key_libraries": arrOf("A few notable libraries that define the project, not every dependency."),
		"build":         prop("string", "How to build it, e.g. `make build` or `go build ./...`."),
		"run":           prop("string", "How to run it in development, e.g. `make dev`."),
		"test":          prop("string", "How to run the tests, if there is a standard command."),
		"notes":         prop("string", "Anything else essential to know (architecture, gotchas). Keep it short."),
	}, "summary")
}

// projectInfo is the stored shape, mirrored by the sidebar UI. Kept in Meta as a
// plain map via JSON round-trip; the field names below are the map keys.
type projectInfo struct {
	Summary      string   `json:"summary,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	Frameworks   []string `json:"frameworks,omitempty"`
	KeyLibraries []string `json:"key_libraries,omitempty"`
	Build        string   `json:"build,omitempty"`
	Run          string   `json:"run,omitempty"`
	Test         string   `json:"test,omitempty"`
	Notes        string   `json:"notes,omitempty"`
}

func (projectInfoTool) Execute(ctx context.Context, in Input) Result {
	var args projectInfo
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Store == nil {
		return Errorf("the store is not available in this runtime")
	}
	if strings.TrimSpace(args.Summary) == "" {
		return Errorf("summary is required")
	}

	sess, err := in.Deps.Store.GetSession(ctx, in.SessionID)
	if err != nil {
		return Errorf("cannot load session: %v", err)
	}
	if pd, _ := sess.Meta["project_dir"].(string); strings.TrimSpace(pd) == "" {
		return Errorf("project_info only applies to a project session (this chat is not bound to a project folder)")
	}

	if sess.Meta == nil {
		sess.Meta = map[string]any{}
	}
	// Store as a plain map so it JSON-serialises cleanly in the Meta blob and the
	// sidebar reads it back with the same keys. Empty fields are omitted.
	info := map[string]any{}
	if s := strings.TrimSpace(args.Summary); s != "" {
		info["summary"] = s
	}
	if v := cleanList(args.Languages); len(v) > 0 {
		info["languages"] = v
	}
	if v := cleanList(args.Frameworks); len(v) > 0 {
		info["frameworks"] = v
	}
	if v := cleanList(args.KeyLibraries); len(v) > 0 {
		info["key_libraries"] = v
	}
	for k, v := range map[string]string{
		"build": args.Build, "run": args.Run, "test": args.Test, "notes": args.Notes,
	} {
		if s := strings.TrimSpace(v); s != "" {
			info[k] = s
		}
	}
	sess.Meta["project_info"] = info

	if err := in.Deps.Store.UpdateSession(ctx, sess); err != nil {
		return Errorf("cannot save project info: %v", err)
	}
	return Text("Project info updated — it now shows in the project sidebar.")
}

// cleanList trims entries and drops blanks, so a sloppy list stays tidy.
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// arrOf builds a JSON-schema for a string array field.
func arrOf(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}
}
