package tools

import (
	"context"
	"strings"

	"github.com/enowdev/antares/internal/secret"
	"github.com/enowdev/antares/internal/socialimap"
)

// emailReadTool lets the Social Media agent read the configured IMAP inbox for
// verification emails and OTP codes.
type emailReadTool struct{}

func (emailReadTool) Name() string { return "email_read" }
func (emailReadTool) Description() string {
	return "Read recent emails from the configured Gmail/IMAP inbox. Use this to retrieve verification links and OTP codes during social media account creation. Returns subject, from, date, and a body snippet for each message."
}
func (emailReadTool) RequiresApproval() bool { return false }

func (emailReadTool) Schema() map[string]any {
	return schema(map[string]any{
		"count": propDefault("integer", "Number of recent emails to retrieve.", 10),
	})
}

func (emailReadTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Count int `json:"count"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.Count <= 0 {
		args.Count = 10
	}
	if args.Count > 50 {
		args.Count = 50
	}

	if in.Deps == nil || in.Deps.Config == nil {
		return Errorf("no configuration available")
	}
	cfg := in.Deps.Config

	host := cfg.Social.IMAPHost
	if host == "" {
		host = "imap.gmail.com"
	}
	port := cfg.Social.IMAPPort
	if port == 0 {
		port = 993
	}
	username := cfg.Social.IMAPUsername
	if username == "" {
		return Errorf("IMAP username is not configured. Set up Gmail in the Social Media page first.")
	}

	if in.Deps.Store == nil {
		return Errorf("storage is not available")
	}
	encPass, err := in.Deps.Store.GetKV(ctx, "social:imap_password")
	if err != nil {
		return Errorf("cannot read IMAP credentials: %v", err)
	}
	if encPass == "" {
		return Errorf("IMAP password is not configured. Set up Gmail in the Social Media page first.")
	}

	key, err := secret.SocialDefault()
	if err != nil {
		return Errorf("social encryption is not set up: %v", err)
	}
	box, err := key.Box()
	if err != nil {
		return Errorf("encryption error: %v", err)
	}
	password, err := box.Decrypt(encPass)
	if err != nil {
		return Errorf("cannot decrypt IMAP password: %v", err)
	}

	messages, err := socialimap.Config{Host: host, Port: port, Username: username, Password: password}.FetchRecent(args.Count)
	if err != nil {
		return Errorf("cannot read inbox: %v", err)
	}

	if len(messages) == 0 {
		return Result{Content: "Inbox is empty."}
	}

	var b strings.Builder
	b.WriteString("Recent emails from inbox:\n\n")
	for _, m := range messages {
		b.WriteString("---\n")
		if m.Subject != "" {
			b.WriteString("Subject: " + m.Subject + "\n")
		}
		if m.From != "" {
			b.WriteString("From: " + m.From + "\n")
		}
		if !m.Date.IsZero() {
			b.WriteString("Date: " + m.Date.Format("2006-01-02 15:04 MST") + "\n")
		}
		if m.Snippet != "" {
			snippet := m.Snippet
			if len(snippet) > 500 {
				snippet = snippet[:500] + "..."
			}
			b.WriteString("Body snippet:\n" + snippet + "\n")
		}
		b.WriteString("\n")
	}
	return Result{Content: b.String()}
}

var _ Tool = emailReadTool{}
