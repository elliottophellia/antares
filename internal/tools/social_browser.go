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
	return "Check the status of the persistent social media browser. The browser holds all social media login sessions with a stable fingerprint. Use 'start' to launch it, 'stop' to close it, or 'status' (default) to check if it's running."
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

	state, errMsg := in.Deps.SocialBrowser.Status()

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
			return Result{Content: "Browser is already running."}
		}
		// The actual Start is handled via the API; the tool announces intent.
		return Result{Content: "Browser start requested. Use the Social Media page or the API to launch it. The browser will appear as a visible window you can interact with."}

	case "stop":
		return Result{Content: "Browser stop requested. Use the Social Media page or the API to stop it."}

	default:
		return Errorf("unknown action %q; use status, start, or stop", action)
	}
}

var _ Tool = socialBrowserTool{}
