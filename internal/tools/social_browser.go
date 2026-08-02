package tools

import (
	"context"
	"fmt"
	"strings"
)

// socialBrowserTool lets the Social Media agent check and control the persistent
// social media browser. It wraps the SocialBrowserManager from Deps.
type socialBrowserTool struct{}

func (socialBrowserTool) Name() string { return "social_browser" }
func (socialBrowserTool) Description() string {
	return "Start, stop, or check the persistent social media browser. This browser holds all social media login sessions with a stable fingerprint. Actions: 'start' (launch visible browser), 'stop' (close browser), or 'status' (default). The browser is shared — you and the user can both see and interact with it."
}
func (socialBrowserTool) RequiresApproval() bool { return true }

func (socialBrowserTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propDefault("string", "What to do: status (default), start, or stop.", "status"),
	})
}

func (socialBrowserTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Action string `json:"action"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	action := strings.TrimSpace(strings.ToLower(args.Action))
	if action == "" {
		action = "status"
	}

	if in.Deps == nil || in.Deps.SocialBrowser == nil {
		return Errorf("social browser is not available")
	}

	mgr := in.Deps.SocialBrowser
	state, errMsg := mgr.Status()

	switch action {
	case "status":
		result := fmt.Sprintf("Browser state: %s", state)
		if errMsg != "" {
			result += fmt.Sprintf("\nError: %s", errMsg)
		}
		if state == "running" {
			result += "\nThe browser is open and visible. All social media login sessions are available."
		} else if state == "stopped" {
			result += "\nUse action 'start' to launch the browser."
		}
		return Result{Content: result, Meta: map[string]any{"state": state}}

	case "start":
		if state == "running" {
			return Result{Content: "Browser is already running. All social media login sessions are available in the visible window."}
		}
		if err := mgr.Start(ctx); err != nil {
			return Errorf("failed to start browser: %v", err)
		}
		return Result{Content: "Browser started successfully. The browser window is now visible and ready. All social media login sessions are available."}

	case "stop":
		mgr.Stop()
		return Result{Content: "Browser stopped."}

	default:
		return Errorf("unknown action %q; use status, start, or stop", action)
	}
}

var _ Tool = socialBrowserTool{}
