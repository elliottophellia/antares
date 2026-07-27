package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/tools"
)

// Approval gates tools that change something. `tools.approval_mode` decides
// what happens: run it, refuse it, or ask a person.
//
// Asking only works where a person is watching. A cron job or a messaging
// thread has nobody to answer, so a request that goes unanswered is refused
// when its deadline passes — the failure mode has to be "did not happen",
// never "happened without being asked".

// ApprovalRequest is one pending decision.
type ApprovalRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	// Reason names why this needs asking about, when it is more than the tool
	// simply being one that writes.
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	decided chan bool
}

type approvalDesk struct {
	mu      sync.Mutex
	pending map[string]*ApprovalRequest
}

var approvals = approvalDesk{pending: map[string]*ApprovalRequest{}}

// PendingApprovals lists what is waiting, oldest first.
func (a *Agent) PendingApprovals() []ApprovalRequest {
	approvals.mu.Lock()
	defer approvals.mu.Unlock()
	out := make([]ApprovalRequest, 0, len(approvals.pending))
	for _, r := range approvals.pending {
		out = append(out, ApprovalRequest{
			ID: r.ID, SessionID: r.SessionID, Tool: r.Tool,
			Arguments: r.Arguments, Reason: r.Reason, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

// ResolveApproval answers a pending request. It reports false when the id is
// unknown, which usually means it already timed out.
func (a *Agent) ResolveApproval(id string, allow bool) bool {
	approvals.mu.Lock()
	r, ok := approvals.pending[id]
	if ok {
		delete(approvals.pending, id)
	}
	approvals.mu.Unlock()
	if !ok {
		return false
	}
	// Buffered, so answering never blocks even if the waiter has given up.
	r.decided <- allow
	return true
}

// approvalTimeout is how long a request waits before being refused.
const approvalTimeout = 5 * time.Minute

// checkApproval decides whether a call may proceed. It returns an error result
// to hand back to the model when it may not.
func (a *Agent) checkApproval(ctx context.Context, call llm.ToolCall, tool tools.Tool, sessionID string, emit Emit) *tools.Result {
	mode := strings.ToLower(strings.TrimSpace(a.cfg.Tools.ApprovalMode))
	if mode == "" {
		mode = "auto"
	}

	danger := dangerIn(call.Name, call.Arguments)

	switch mode {
	case "auto":
		// The mode says run it, so it runs. A destructive command is still
		// worth naming in the transcript — silently doing it is the thing
		// nobody wants to discover afterwards.
		if danger != "" {
			_ = emit(Event{Type: EventNotice, Message: "running a command that " + danger})
		}
		return nil

	case "deny":
		if tools.NeedsApproval(tool) || danger != "" {
			res := tools.Errorf("refused: %s changes state and tools.approval_mode is \"deny\". "+
				"Report what you would have done instead.", call.Name)
			return &res
		}
		return nil

	case "prompt":
		if !tools.NeedsApproval(tool) && danger == "" {
			return nil
		}

	default:
		// An unrecognised mode is treated as the safest one that still works.
		if !tools.NeedsApproval(tool) && danger == "" {
			return nil
		}
	}

	req := &ApprovalRequest{
		ID:        newID("apr"),
		SessionID: sessionID,
		Tool:      call.Name,
		Arguments: call.Arguments,
		Reason:    danger,
		CreatedAt: time.Now(),
		decided:   make(chan bool, 1),
	}
	approvals.mu.Lock()
	approvals.pending[req.ID] = req
	approvals.mu.Unlock()

	payload, _ := json.Marshal(req)
	_ = emit(Event{
		Type:      EventApproval,
		ID:        req.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
		Message:   approvalMessage(req),
		Content:   string(payload),
	})

	defer func() {
		approvals.mu.Lock()
		delete(approvals.pending, req.ID)
		approvals.mu.Unlock()
	}()

	select {
	case allow := <-req.decided:
		if allow {
			_ = emit(Event{Type: EventNotice, Message: "approved " + call.Name})
			return nil
		}
		res := tools.Errorf("the user refused this %s call. Do not retry it; "+
			"ask what to do instead, or continue without it.", call.Name)
		return &res

	case <-time.After(approvalTimeout):
		res := tools.Errorf("no one approved this %s call within %s, so it did not run. "+
			"Say what you were about to do and stop.", call.Name, approvalTimeout)
		return &res

	case <-ctx.Done():
		res := tools.Errorf("interrupted before %s was approved", call.Name)
		return &res
	}
}

func approvalMessage(r *ApprovalRequest) string {
	if r.Reason != "" {
		return fmt.Sprintf("%s wants to run, and %s", r.Tool, r.Reason)
	}
	return r.Tool + " wants to change something"
}

// dangerous names commands that are worth stopping for even when approval is
// otherwise off. Each entry says, in words that finish "…and it", what the
// command does — the message is only useful if it explains the risk.
var dangerous = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+)+(/|~|\$HOME|\*)(\s|$)`), "it deletes a whole tree from your home or root"},
	{regexp.MustCompile(`\bmkfs(\.\w+)?\b`), "it formats a filesystem"},
	{regexp.MustCompile(`\bdd\b[^\n]*\bof=/dev/`), "it writes directly to a device"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|hd)`), "it writes directly to a disk"},
	{regexp.MustCompile(`:\(\)\s*\{\s*:\|\s*:&\s*\}\s*;\s*:`), "it is a fork bomb"},
	{regexp.MustCompile(`\bchmod\s+(-R\s+)?777\s+/`), "it makes system paths world-writable"},
	{regexp.MustCompile(`\bgit\s+push\b[^\n]*(--force|-f)\b`), "it force-pushes, which can destroy history others have"},
	{regexp.MustCompile(`\bgit\s+reset\s+--hard\b`), "it discards uncommitted work"},
	{regexp.MustCompile(`\b(DROP|TRUNCATE)\s+(TABLE|DATABASE|SCHEMA)\b`), "it drops a database object"},
	{regexp.MustCompile(`\bcurl\b[^\n|]*\|\s*(sudo\s+)?(ba)?sh`), "it pipes a download straight into a shell"},
	{regexp.MustCompile(`\bwget\b[^\n|]*\|\s*(sudo\s+)?(ba)?sh`), "it pipes a download straight into a shell"},
	{regexp.MustCompile(`\bsudo\b`), "it runs as root"},
}

// dangerIn reports why a call is destructive, or an empty string when it is
// ordinary. Only the terminal is scanned: it is the tool that can do anything.
func dangerIn(toolName, arguments string) string {
	if toolName != "terminal" {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return ""
	}
	cmd := args.Command
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	for _, d := range dangerous {
		if d.re.MatchString(cmd) {
			return d.why
		}
	}
	return ""
}
