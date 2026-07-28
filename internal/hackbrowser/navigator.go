// Navigator: the LLM-driven planner.
//
// Given a PlannerSnapshot (the page as the model sees it), the navigator
// calls the model with the planner system prompt and parses the response
// into a PagePlan: what to click, what to fill in, what to revisit.
//
// In the TypeScript original this used Vercel's AI SDK with @ai-sdk/openai
// and @ai-sdk/anthropic; here it uses antares' own internal/llm Client
// abstraction, which already knows how to talk to every provider antares
// supports and centralises retry / cost tracking.

package hackbrowser

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/llm"
)

//go:embed data/planner.txt
var plannerPrompt string

// PlannerLogger is the per-service log handle for navigator code.
var plannerLog = Log.Create("hackbrowser:navigator")

// MaxPlannerOutputTokens caps the planner response. The plan is small JSON;
// 16K is generous headroom for reasoning models that emit a chain-of-thought
// prefix before the JSON body.
const MaxPlannerOutputTokens = 16384

// Planner is the contract a per-page planner satisfies. Implementations
// wrap an llm.Client; tests can substitute a stub.
type Planner interface {
	// Plan asks the model what to do with the current page snapshot.
	// Returns a validated PagePlan; an empty plan when the model declines.
	Plan(ctx context.Context, snapshot PlannerSnapshot) (PagePlan, error)
}

// LLMPlanner is the production planner — wraps an llm.Client.
type LLMPlanner struct {
	Model  llm.Client
	Usage  *CrawlUsage // optional accumulator
}

// Plan implements Planner. One LLM call per page.
func (p *LLMPlanner) Plan(ctx context.Context, snapshot PlannerSnapshot) (PagePlan, error) {
	return p.call(ctx, snapshot, "")
}

// PlanUnexplored asks the model to plan only for elements the first pass
// skipped. The agent uses this as a follow-up when the first plan left
// elements untouched (System Observes, LLM Interprets).
func (p *LLMPlanner) PlanUnexplored(ctx context.Context, snapshot PlannerSnapshot, unexplored []string) (PagePlan, error) {
	return p.call(ctx, snapshot, strings.Join(unexplored, "\n"))
}

// call does one Chat completion and parses the result.
func (p *LLMPlanner) call(ctx context.Context, snapshot PlannerSnapshot, extraInstruction string) (PagePlan, error) {
	userPayload := snapshot
	if extraInstruction != "" {
		// Trim the snapshot's elements down to those still unexplored.
		keep := map[string]bool{}
		for _, label := range splitLines(extraInstruction) {
			keep[label] = true
		}
		filtered := make([]PromptElement, 0, len(userPayload.Elements))
		for _, e := range userPayload.Elements {
			if keep[e.Label] {
				filtered = append(filtered, e)
			}
		}
		userPayload.Elements = filtered
	}
	userMessage, _ := json.Marshal(userPayload)

	req := llm.Request{
		System:    plannerPrompt,
		Messages:  []llm.Message{{Role: "user", Content: string(userMessage)}},
		MaxTokens: MaxPlannerOutputTokens,
		// Zero temperature for deterministic plans.
		Temperature: 0,
	}

	// First attempt; on transient failure, retry once.
	resp, err := p.Model.Chat(ctx, req)
	if err != nil && isAuthError(err) {
		return PagePlan{}, err
	}
	if err != nil {
		plannerLog.Warn("planPage failed, retrying once", F("err", err.Error()))
		resp, err = p.Model.Chat(ctx, req)
		if err != nil && isAuthError(err) {
			return PagePlan{}, err
		}
		if err != nil {
			plannerLog.Error("planPage failed after retry, returning empty plan", F("err", err.Error()))
			return PagePlan{}, nil
		}
	}

	if p.Usage != nil {
		p.Usage.InputTokens += resp.Usage.InputTokens
		p.Usage.OutputTokens += resp.Usage.OutputTokens
		p.Usage.CacheReadTokens += resp.Usage.CacheReadTokens
		p.Usage.CacheWriteTokens += resp.Usage.CacheWriteTokens
	}

	return parsePlan(resp.Content)
}

// parsePlan extracts the JSON object from the model's response and validates
// it into a PagePlan. Tolerates a leading chain-of-thought by taking the
// first "{" to the last "}".
func parsePlan(raw string) (PagePlan, error) {
	trimmed := strings.TrimSpace(raw)
	plannerLog.Debug("planner response", F("len", len(trimmed)), F("preview", firstN(trimmed, 500)))
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 || end <= start {
		// Model declined — empty plan, not an error.
		return PagePlan{}, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &parsed); err != nil {
		return PagePlan{}, fmt.Errorf("plan json: %w", err)
	}
	return validatePlan(parsed), nil
}

// validatePlan coerces the model's JSON into a typed PagePlan, dropping
// malformed entries rather than failing the whole plan.
func validatePlan(raw map[string]any) PagePlan {
	plan := PagePlan{}
	if tasks, ok := raw["tasks"].([]any); ok {
		for _, t := range tasks {
			task, ok := t.(map[string]any)
			if !ok {
				continue
			}
			pt := PageTask{TriggersMutation: stringOrEmpty(task["triggersMutation"])}
			switch strVal(task["type"]) {
			case "form":
				fields := []FormFieldPlan{}
				if arr, ok := task["fields"].([]any); ok {
					for _, f := range arr {
						fm, ok := f.(map[string]any)
						if !ok {
							continue
						}
						fields = append(fields, FormFieldPlan{
							Role:  stringOrEmpty(fm["role"]),
							Label: stringOrEmpty(fm["label"]),
							Value: stringOrEmpty(fm["value"]),
						})
					}
				}
				if len(fields) == 0 {
					continue
				}
				sub, _ := task["submit"].(map[string]any)
				if sub == nil {
					continue
				}
				pt.Type = "form"
				pt.Fields = fields
				subRef := FormSubmitRef{
					Role:  stringOrEmpty(sub["role"]),
					Label: stringOrEmpty(sub["label"]),
				}
				if subRef.Role == "" {
					subRef.Role = "button"
				}
				pt.Submit = &subRef
				plan.Tasks = append(plan.Tasks, pt)
			case "click":
				pt.Type = "click"
				pt.Role = stringOrEmpty(task["role"])
				pt.Label = stringOrEmpty(task["label"])
				pt.Reason = stringOrEmpty(task["reason"])
				if pt.Role == "" || pt.Label == "" {
					continue
				}
				plan.Tasks = append(plan.Tasks, pt)
			}
		}
	}
	// Intelligence fields (PagePlan v2).
	plan.PageState = validatePageState(raw["pageState"])
	plan.RevisitAfter = validateRevisit(raw["revisitAfter"])
	if r, ok := raw["revisitReason"].(string); ok && r != "" {
		plan.RevisitReason = r
	}
	if r, ok := raw["revisitOn"].(string); ok && r != "" {
		plan.RevisitOn = r
	}
	// Refinement: pageState=empty requires revisitReason. Downgrade to
	// "unknown" on LLM drift so the agent doesn't loop on an empty page.
	if plan.PageState == PageStateEmpty && plan.RevisitReason == "" {
		plannerLog.Warn("pageState=empty without revisitReason — downgrading to unknown")
		plan.PageState = PageStateUnknown
		plan.RevisitAfter = ""
		plan.RevisitOn = ""
	}
	return plan
}

func validatePageState(v any) PageStateKind {
	switch v {
	case "populated":
		return PageStatePopulated
	case "empty":
		return PageStateEmpty
	case "unknown":
		return PageStateUnknown
	}
	return ""
}

func validateRevisit(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok && s == "any-mutation" {
		return s
	}
	return ""
}

// isAuthError reports whether err looks like an auth failure that will
// never recover within a single run. Auth errors must propagate so the
// caller records them in CrawlResult.errors — masking them would make a
// crawl with broken credentials finish as a clean run that found nothing.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "401") || strings.Contains(msg, "403") {
		return true
	}
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") {
		return true
	}
	if strings.Contains(msg, "api key") || strings.Contains(msg, "apikey") {
		return true
	}
	return false
}

// stringOrEmpty coerces an interface{} (typically from JSON) into a string,
// treating nil and empty as "". JSON numbers are stringified.
func stringOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		// JSON numbers come back as float64. Format without trailing zeros.
		return fmt.Sprintf("%g", x)
	}
	// Fall back to fmt; unlikely path for plan JSON.
	return fmt.Sprintf("%v", v)
}

// strVal returns the string form of v.(string), or "" if v isn't a string.
func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// splitLines trims and splits a multiline string into non-empty lines.
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// firstN returns the first n bytes of s, with "…" appended on truncation.
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ErrNoModel is returned when no provider is available for the planner.
var ErrNoModel = errors.New("hackbrowser: no LLM provider configured for the planner")
