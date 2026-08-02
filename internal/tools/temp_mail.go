package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/tempmail"
)

// tempMailTool gives agents disposable generator.email inboxes without UI state.
type tempMailTool struct{}

func (tempMailTool) Name() string { return "temp_mail" }
func (tempMailTool) Description() string {
	return "Create and read public disposable generator.email inboxes for one-time account verification. " +
		"Actions: domains lists available domains; create mints an address; messages reads an inbox; " +
		"wait_code waits for an OTP or verification code. Inboxes are public to anyone who knows the address."
}
func (tempMailTool) RequiresApproval() bool { return false }

func (tempMailTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":          propEnum("Operation to perform.", "domains", "create", "messages", "wait_code"),
		"domain":          prop("string", "Domain for create. Get one with the domains action."),
		"address":         prop("string", "Disposable email address for messages or wait_code."),
		"timeout_seconds": propDefault("integer", "Maximum time to wait for a code, from 1 to 300 seconds.", 90),
	}, "action")
}

func (tempMailTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Action         string `json:"action"`
		Domain         string `json:"domain"`
		Address        string `json:"address"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}

	generator := tempmail.NewGenerator(nil)
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "domains":
		domains, err := generator.Domains(ctx)
		if err != nil {
			return Errorf("cannot list temporary mail domains: %v", err)
		}
		if len(domains) == 0 {
			return Text("No temporary mail domains are currently available.")
		}
		return Text("Available temporary mail domains:\n- " + strings.Join(domains, "\n- "))

	case "create":
		address, err := generator.Generate(ctx, args.Domain)
		if err != nil {
			return Errorf("cannot create temporary email: %v", err)
		}
		return Result{
			Content: fmt.Sprintf("Temporary email created: %s\nThis inbox is public. Use it only for short-lived verification.", address),
			Meta:    map[string]any{"address": address, "domain": strings.TrimSpace(strings.TrimPrefix(args.Domain, "@"))},
		}

	case "messages":
		if strings.TrimSpace(args.Address) == "" {
			return Errorf("address is required for messages")
		}
		messages, err := generator.Messages(ctx, args.Address)
		if err != nil {
			return Errorf("cannot read temporary inbox: %v", err)
		}
		if len(messages) == 0 {
			return Text("Temporary inbox is empty.")
		}
		body, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			return Errorf("cannot format temporary inbox: %v", err)
		}
		return Text(string(body))

	case "wait_code":
		if strings.TrimSpace(args.Address) == "" {
			return Errorf("address is required for wait_code")
		}
		if args.TimeoutSeconds == 0 {
			args.TimeoutSeconds = 90
		}
		if args.TimeoutSeconds < 1 || args.TimeoutSeconds > 300 {
			return Errorf("timeout_seconds must be between 1 and 300")
		}
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(args.TimeoutSeconds)*time.Second)
		defer cancel()
		code, err := generator.WaitForCode(waitCtx, args.Address)
		if err != nil {
			if waitCtx.Err() != nil {
				return Errorf("no verification code arrived within %d seconds", args.TimeoutSeconds)
			}
			return Errorf("cannot wait for verification code: %v", err)
		}
		return Result{Content: "Verification code: " + code, Meta: map[string]any{"code": code, "address": args.Address}}

	default:
		return Errorf("unknown action %q; use domains, create, messages, or wait_code", args.Action)
	}
}

var _ Tool = tempMailTool{}
var _ Approval = tempMailTool{}
