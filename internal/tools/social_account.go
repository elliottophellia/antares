package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/secret"
	"github.com/enowdev/antares/internal/store"
)

// socialAccountTool lets the agent save and list social media accounts with
// encrypted credentials. After creating an account on a platform, the agent
// calls this tool to persist the username, password, and recovery codes.
type socialAccountTool struct{}

func (socialAccountTool) Name() string { return "social_account" }
func (socialAccountTool) Description() string {
	return "Save or list social media accounts with encrypted credentials. After creating an account on any platform, use action 'save' to store the username, password, recovery codes, and profile URL. Use action 'list' to see all saved accounts. Passwords are encrypted at rest — never store them in files, RAG, or skills."
}
func (socialAccountTool) RequiresApproval() bool { return false }

func (socialAccountTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":   propDefault("string", "save or list (default: list).", "list"),
		"platform": prop("string", "Platform name (instagram, facebook, threads, x, etc.). Required for save."),
		"username": prop("string", "Account username or email. Required for save."),
		"password": prop("string", "Account password. Required for save."),
		"display_name": prop("string", "Display name on the platform."),
		"recovery_codes": prop("string", "Recovery codes, one per line or comma-separated."),
		"profile_url": prop("string", "Profile URL on the platform."),
		"status": propDefault("string", "Account status: connected, not_created, pending, verification_required, suspended, error.", "connected"),
	})
}

func (socialAccountTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Action        string `json:"action"`
		Platform      string `json:"platform"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		DisplayName   string `json:"display_name"`
		RecoveryCodes string `json:"recovery_codes"`
		ProfileURL    string `json:"profile_url"`
		Status        string `json:"status"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}

	action := strings.TrimSpace(strings.ToLower(args.Action))
	if action == "" {
		action = "list"
	}

	if in.Deps == nil || in.Deps.Store == nil {
		return Errorf("storage is not available")
	}
	db := in.Deps.Store

	switch action {
	case "list":
		if !secret.SocialAvailable() {
			return Result{Content: "Social encryption is not set up. No accounts saved."}
		}
		accounts, err := db.ListSocialAccounts(ctx)
		if err != nil {
			return Errorf("cannot list accounts: %v", err)
		}
		if len(accounts) == 0 {
			return Result{Content: "No social accounts saved yet."}
		}
		var b strings.Builder
		b.WriteString("Saved social media accounts:\n\n")
		for _, a := range accounts {
			b.WriteString(fmt.Sprintf("- %s (@%s) [%s]\n", a.Platform, a.Username, a.Status))
			if a.DisplayName != "" {
				b.WriteString(fmt.Sprintf("  Display name: %s\n", a.DisplayName))
			}
			if a.ProfileURL != "" {
				b.WriteString(fmt.Sprintf("  Profile: %s\n", a.ProfileURL))
			}
			b.WriteString("\n")
		}
		return Result{Content: b.String()}

	case "save":
		if strings.TrimSpace(args.Platform) == "" || strings.TrimSpace(args.Username) == "" {
			return Errorf("platform and username are required for save")
		}
		if !secret.SocialAvailable() {
			return Errorf("social encryption is not set up — generate a master key in the Social Media page first")
		}
		if args.Status == "" {
			args.Status = "connected"
		}

		acct := &store.SocialAccount{
			ID:            newAccountID(),
			Platform:      args.Platform,
			DisplayName:   args.DisplayName,
			Username:      args.Username,
			Password:      args.Password,
			RecoveryCodes: args.RecoveryCodes,
			ProfileURL:    args.ProfileURL,
			Status:        args.Status,
			RAGNamespace:  "social/" + args.Platform,
			SkillName:     "social-" + args.Platform,
			CreatedAt:     time.Now(),
			UpdatedAt:    time.Now(),
		}
		if err := db.PutSocialAccount(ctx, acct); err != nil {
			return Errorf("cannot save account: %v", err)
		}
		return Result{
			Content: fmt.Sprintf("Account saved: %s (@%s) [%s]. Credentials encrypted at rest.", acct.Platform, acct.Username, acct.Status),
			Meta:    map[string]any{"id": acct.ID, "platform": acct.Platform, "username": acct.Username},
		}

	default:
		return Errorf("unknown action %q; use save or list", action)
	}
}

func newAccountID() string {
	return fmt.Sprintf("soc_%d", time.Now().UnixNano())
}

var _ Tool = socialAccountTool{}
