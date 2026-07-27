package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

// cmdTitle renames the current conversation. Auto-generated titles are a guess
// from the first message and are often wrong by the end.
func cmdTitle(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no conversation to name yet")
	}
	sess, err := d.Store.GetSession(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	title := strings.TrimSpace(in.Args)
	if title == "" {
		return Result{Output: fmt.Sprintf("This conversation is called %q. Rename it with `/title <name>`.",
			orDash(sess.Title))}, nil
	}
	sess.Title = title
	if err := d.Store.UpdateSession(ctx, sess); err != nil {
		return Result{}, err
	}
	return Result{Output: "Renamed to " + title + ".", Action: Action{Kind: "session-changed"}}, nil
}

// cmdFork copies a conversation so you can try a different direction without
// losing the one you have. Both halves keep the history up to this point.
func cmdFork(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no conversation to fork")
	}

	source, err := d.Store.GetSession(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	messages, err := d.Store.ListMessages(ctx, in.SessionID, 0, 0)
	if err != nil {
		return Result{}, err
	}

	title := strings.TrimSpace(in.Args)
	if title == "" {
		title = source.Title + " (fork)"
	}
	fork := &store.Session{
		ID:        newSessionID(),
		Title:     title,
		Model:     source.Model,
		Provider:  source.Provider,
		Workspace: source.Workspace,
		Platform:  source.Platform,
		UserID:    source.UserID,
	}
	if err := d.Store.CreateSession(ctx, fork); err != nil {
		return Result{}, err
	}

	// Copy the transcript verbatim. New ids, because a message belongs to one
	// session and the two histories diverge from here.
	for i := range messages {
		m := messages[i]
		m.ID = newMessageID()
		m.SessionID = fork.ID
		if err := d.Store.AppendMessage(ctx, &m); err != nil {
			return Result{}, err
		}
	}

	return Result{
		Output: fmt.Sprintf("Forked into **%s** (`%s`) with %d message(s).\n\n"+
			"You are now in the fork; the original is untouched.", title, shortID(fork.ID), len(messages)),
		Action: Action{Kind: "resume", Value: fork.ID},
	}, nil
}

// cmdUndo removes the last exchange, so a question that went wrong can be
// asked again without the bad answer still in the history.
func cmdUndo(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is nothing to undo")
	}
	messages, err := d.Store.ListMessages(ctx, in.SessionID, 0, 0)
	if err != nil {
		return Result{}, err
	}
	if len(messages) == 0 {
		return Result{Output: "This conversation is empty."}, nil
	}

	// Walk back to the last user message; everything from there was the reply
	// to it, including any tool calls it made.
	cut := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == store.RoleUser {
			cut = i
			break
		}
	}
	if cut < 0 {
		return Result{Output: "There is no exchange to remove."}, nil
	}

	removed := 0
	var lastUser string
	for _, m := range messages[cut:] {
		if m.Role == store.RoleUser && lastUser == "" {
			lastUser = m.Content
		}
		if err := d.Store.DeleteMessage(ctx, m.ID); err != nil {
			return Result{}, err
		}
		removed++
	}

	out := fmt.Sprintf("Removed the last exchange (%d message(s)).", removed)
	if lastUser != "" {
		out += "\n\nWhat you had asked:\n\n> " + firstLine(lastUser)
	}
	return Result{Output: out, Action: Action{Kind: "session-changed"}}, nil
}

// cmdExport writes a conversation to a file you can keep or read elsewhere.
func cmdExport(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no conversation to export")
	}

	format := strings.ToLower(strings.TrimSpace(in.Args))
	if format == "" {
		format = "markdown"
	}
	if format != "markdown" && format != "md" && format != "json" {
		return Result{}, fmt.Errorf("unknown format %q — use markdown or json", format)
	}

	sess, err := d.Store.GetSession(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	messages, err := d.Store.ListMessages(ctx, in.SessionID, 0, 0)
	if err != nil {
		return Result{}, err
	}

	dir := config.Path("exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}

	name := sanitise(sess.Title)
	if name == "" {
		name = shortID(sess.ID)
	}
	var (
		path string
		body []byte
	)
	if format == "json" {
		path = filepath.Join(dir, name+".json")
		body, err = json.MarshalIndent(map[string]any{
			"session":     sess,
			"messages":    messages,
			"exported_at": time.Now(),
		}, "", "  ")
		if err != nil {
			return Result{}, err
		}
	} else {
		path = filepath.Join(dir, name+".md")
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", orDash(sess.Title))
		fmt.Fprintf(&b, "_%s · %s_\n\n", sess.Model, sess.CreatedAt.Format("2 January 2006, 15:04"))
		for _, m := range messages {
			switch m.Role {
			case store.RoleUser:
				fmt.Fprintf(&b, "## You\n\n%s\n\n", m.Content)
			case store.RoleAssistant:
				if m.Content != "" {
					fmt.Fprintf(&b, "## Antares\n\n%s\n\n", m.Content)
				}
			case store.RoleTool:
				fmt.Fprintf(&b, "> `%s` → %s\n\n", m.ToolName, firstLine(m.Content))
			}
		}
		body = []byte(b.String())
	}

	if err := os.WriteFile(path, body, 0o644); err != nil {
		return Result{}, err
	}
	return Result{Output: fmt.Sprintf("Exported %d message(s) to `%s`.", len(messages), path)}, nil
}

// sanitise turns a title into a filename.
func sanitise(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == ' ':
			return '-'
		}
		return -1
	}, s)
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// newSessionID and newMessageID mirror the ids the agent creates, so a forked
// conversation is indistinguishable from one that was started normally.
func newSessionID() string { return randomID("ses") }
func newMessageID() string { return randomID("msg") }

func randomID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
