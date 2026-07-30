package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/vps"
)

// vpsRunTool runs a shell command on one of the user's saved VPS hosts over SSH
// and returns its output. It is the muscle behind the "VPS manager" skill:
// inspect services, read logs, restart things, deploy, update packages — the
// agent decides the command, this executes it on the chosen host.
type vpsRunTool struct{}

func (vpsRunTool) Name() string { return "vps_run" }
func (vpsRunTool) Description() string {
	return "Run a shell command on one of the user's saved VPS servers over SSH and return its output. " +
		"Call with no command to list the available servers (id + label + host). Pass `vps` as a server's id " +
		"or label. Use it to inspect and manage a server — systemctl, journalctl, docker, df, apt/yum, deploys, " +
		"edits. Commands run as the configured SSH user; be careful with destructive ones. For authorized use " +
		"on servers the user owns."
}
func (vpsRunTool) Schema() map[string]any {
	return schema(map[string]any{
		"vps":             prop("string", "Which server: its id or label (call with no command to list them)."),
		"command":         prop("string", "The shell command to run. Omit to just list the saved servers."),
		"timeout_seconds": propDefault("integer", "How long to allow the command to run.", 60),
	})
}

// RequiresApproval: running arbitrary commands on a live server is a mutating,
// potentially destructive action — gate it behind approval like the terminal.
func (vpsRunTool) RequiresApproval() bool { return true }

func (vpsRunTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		VPS     string `json:"vps"`
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Store == nil {
		return Errorf("no VPS store available in this runtime")
	}

	hosts, err := in.Deps.Store.ListVPSHosts(ctx)
	if err != nil {
		return Errorf("could not read saved servers: %v", err)
	}
	if len(hosts) == 0 {
		return Text("No servers are saved. Add one on the dashboard's VPS page (host, port, user, and a password or SSH key), then reference it by id or label.")
	}

	// No command → list the servers so the agent can pick one.
	ref := strings.TrimSpace(args.VPS)
	if strings.TrimSpace(args.Command) == "" || ref == "" {
		var b strings.Builder
		fmt.Fprintf(&b, "Saved servers (%d) — pass id or label as `vps`, plus a `command`:\n\n", len(hosts))
		for _, h := range hosts {
			fmt.Fprintf(&b, "  - id=%s  label=%q  %s@%s:%d\n", h.ID, h.Label, h.Username, h.Host, h.Port)
		}
		return Text(b.String())
	}

	// Resolve by id, then case-insensitive label.
	var target *vps.Target
	label, hostID, hadKey := "", "", false
	for i := range hosts {
		h := hosts[i]
		if h.ID == ref || strings.EqualFold(h.Label, ref) {
			t := vps.Target{
				Host: h.Host, Port: h.Port, Username: h.Username, AuthMethod: h.AuthMethod,
				Password: h.Password, PrivateKey: h.PrivateKey, Passphrase: h.Passphrase,
				KnownHostKey: h.HostKey,
			}
			target = &t
			label, hostID, hadKey = h.Label, h.ID, h.HostKey != ""
			break
		}
	}
	if target == nil {
		return Errorf("no saved server matches %q — call vps_run with no command to list them", ref)
	}

	if args.Timeout <= 0 || args.Timeout > 900 {
		args.Timeout = 60
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
	defer cancel()

	in.Emit(Progress{Tool: "vps_run", Message: fmt.Sprintf("running on %s…", label)})
	out, seen, err := vps.Run(runCtx, *target, args.Command)
	// Pin the host key on first successful connect (TOFU).
	if !hadKey && seen != "" && !errors.Is(err, vps.ErrHostKeyChanged) {
		_ = in.Deps.Store.SetVPSHostKey(ctx, hostID, seen)
	}
	out = strings.TrimRight(out, "\n")
	if err != nil {
		// Include whatever output there was (usually stderr) — more useful than
		// the bare error.
		msg := err.Error()
		if out != "" {
			return Result{Content: fmt.Sprintf("Command failed on %s: %s\n\n%s", label, msg, out), IsError: true}
		}
		return Errorf("command failed on %s: %s", label, msg)
	}
	if out == "" {
		out = "(no output)"
	}
	return Text(fmt.Sprintf("On %s:\n\n%s", label, out))
}
