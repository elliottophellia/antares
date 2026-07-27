package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// computerTool controls the desktop GUI — screenshots, mouse, and keyboard —
// for automating applications that have no other interface. It is off unless
// explicitly enabled (tools.computer_use), needs a graphical session, and is
// approval-gated, because it acts as the user on their own machine.
type computerTool struct{}

func (computerTool) Name() string { return "computer" }

func (computerTool) Description() string {
	return "Control the desktop: take a screenshot, move or click the mouse, type text, or press a key. " +
		"Use it only for GUI apps that cannot be driven any other way. Take a screenshot first to see the screen, " +
		"then act on coordinates. Requires a graphical session and the platform helpers (xdotool/scrot on Linux, cliclick on macOS)."
}

func (computerTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propEnum("What to do.", "screenshot", "click", "double_click", "right_click", "move", "type", "key", "scroll"),
		"x":      prop("integer", "For click/move: screen x coordinate."),
		"y":      prop("integer", "For click/move: screen y coordinate."),
		"text":   prop("string", "For type: the text to type."),
		"key":    prop("string", "For key: a key or combo, e.g. Return, Escape, ctrl+c."),
		"amount": propDefault("integer", "For scroll: lines (negative scrolls up).", 3),
	}, "action")
}

// RequiresApproval gates the tool: it acts on the user's live desktop.
func (computerTool) RequiresApproval() bool { return true }

func (computerTool) Execute(ctx context.Context, in Input) Result {
	var cfg *config.Config
	if in.Deps != nil {
		cfg = in.Deps.Config
	}
	if cfg == nil || !cfg.Tools.ComputerUse {
		return Errorf("the computer tool is off. Enable it with tools.computer_use = true (it controls your desktop).")
	}

	var args struct {
		Action string `json:"action"`
		X      int    `json:"x"`
		Y      int    `json:"y"`
		Text   string `json:"text"`
		Key    string `json:"key"`
		Amount int    `json:"amount"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))

	switch action {
	case "screenshot":
		path, err := desktopScreenshot(ctx, in.Workspace)
		if err != nil {
			return Errorf("%v", err)
		}
		return Result{
			Content: fmt.Sprintf("Saved a desktop screenshot to %s. Read it with a vision-capable model, then act on the coordinates.", path),
			Meta:    map[string]any{"path": path},
		}
	case "click", "double_click", "right_click", "move":
		return runComputer(ctx, mouseCommand(action, args.X, args.Y), fmt.Sprintf("%s at %d,%d", action, args.X, args.Y))
	case "type":
		if args.Text == "" {
			return Errorf("text is required to type")
		}
		return runComputer(ctx, typeCommand(args.Text), "typed the text")
	case "key":
		if args.Key == "" {
			return Errorf("key is required")
		}
		return runComputer(ctx, keyCommand(args.Key), "pressed "+args.Key)
	case "scroll":
		if args.Amount == 0 {
			args.Amount = 3
		}
		return runComputer(ctx, scrollCommand(args.Amount), "scrolled")
	default:
		return Errorf("unknown action %q", args.Action)
	}
}

// runComputer executes a platform command, or reports the missing helper.
func runComputer(ctx context.Context, argv []string, ok string) Result {
	if len(argv) == 0 {
		return Errorf("the computer tool does not support this on %s", runtime.GOOS)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return Errorf("%s is not installed — the computer tool needs it on %s", argv[0], runtime.GOOS)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return Errorf("%s failed: %s", argv[0], detail)
	}
	return Text(ok)
}

// mouseCommand builds the platform mouse command for an action.
func mouseCommand(action string, x, y int) []string {
	sx, sy := strconv.Itoa(x), strconv.Itoa(y)
	if runtime.GOOS == "darwin" {
		switch action {
		case "move":
			return []string{"cliclick", "m:" + sx + "," + sy}
		case "double_click":
			return []string{"cliclick", "dc:" + sx + "," + sy}
		case "right_click":
			return []string{"cliclick", "rc:" + sx + "," + sy}
		default:
			return []string{"cliclick", "c:" + sx + "," + sy}
		}
	}
	// Linux / X11 via xdotool.
	base := []string{"xdotool", "mousemove", sx, sy}
	switch action {
	case "move":
		return base
	case "double_click":
		return append(base, "click", "--repeat", "2", "1")
	case "right_click":
		return append(base, "click", "3")
	default:
		return append(base, "click", "1")
	}
}

func typeCommand(text string) []string {
	if runtime.GOOS == "darwin" {
		return []string{"cliclick", "t:" + text}
	}
	return []string{"xdotool", "type", "--clearmodifiers", text}
}

func keyCommand(key string) []string {
	if runtime.GOOS == "darwin" {
		// cliclick key presses are limited; pass through common names.
		return []string{"cliclick", "kp:" + strings.ToLower(key)}
	}
	return []string{"xdotool", "key", key}
}

func scrollCommand(amount int) []string {
	if runtime.GOOS == "darwin" {
		return nil // cliclick has no scroll; unsupported
	}
	button := "5" // down
	n := amount
	if n < 0 {
		button = "4" // up
		n = -n
	}
	return []string{"xdotool", "click", "--repeat", strconv.Itoa(n), button}
}

// desktopScreenshot captures the whole screen with whichever helper is present.
func desktopScreenshot(ctx context.Context, workspace string) (string, error) {
	dir := filepath.Join(config.Home(), "screenshots")
	if workspace != "" {
		if info, err := os.Stat(workspace); err == nil && info.IsDir() {
			dir = filepath.Join(workspace, ".antares", "screenshots")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("desktop-%d.png", time.Now().UnixMilli()))

	var candidates [][]string
	if runtime.GOOS == "darwin" {
		candidates = [][]string{{"screencapture", "-x", path}}
	} else {
		candidates = [][]string{
			{"scrot", "-o", path},
			{"gnome-screenshot", "-f", path},
			{"import", "-window", "root", path},
			{"spectacle", "-b", "-n", "-o", path},
		}
	}
	for _, argv := range candidates {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		if out, err := cmd.CombinedOutput(); err == nil {
			return path, nil
		} else {
			_ = out
		}
	}
	return "", fmt.Errorf("no screenshot helper found — install scrot/gnome-screenshot (Linux) and ensure a display is available")
}
