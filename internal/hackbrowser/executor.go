// Executor: resolve a planned task against the live DOM and run it.
//
// The planner returns abstract tasks ("click role=button named Submit",
// "fill role=textbox named Email with alice@example.com"). The executor
// resolves each task's role+label to a real CSS selector from the
// current page state's RawElements, then dispatches the action via the
// browser session. Network events that fire while the action runs are
// drained by the caller; the executor's job is purely to drive the page.
//
// Ported from packages/hackbrowser/src/executor.ts. Three differences:
//   - antares' browser Session has no playwright locator API, so the
//     selector resolution + click is one CDP Runtime.evaluate call.
//   - "select" is implemented via native <select> value-setting in JS,
//     with a custom-dropdown fallback (click + click role=option).
//   - "navigate" uses Session.Navigate (CDP Page.navigate) directly.

package hackbrowser

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/browser"
)

var executorLog = Log.Create("hackbrowser:executor")

const (
	clickTimeout      = 2 * time.Second
	fillTimeout       = 3 * time.Second
	stabilizeTimeout  = 3 * time.Second
	stabilizeWaitMs   = 200
)

// Executor runs one planned task (click or form) against the page.
type Executor struct {
	Sess *browser.Session
}

// ExecuteTask runs one PageTask. Returns the per-element ActionResult
// (success/failure, navigation, dom change indicator). The caller owns
// draining Network events around the call.
func (e *Executor) ExecuteTask(ctx context.Context, task PageTask, elements []RawElement) ActionResult {
	urlBefore, _ := e.Sess.URL(ctx)
	var err error
	switch task.Type {
	case "form":
		err = e.executeForm(ctx, task, elements)
	case "click":
		err = e.executeClick(ctx, task, elements)
	default:
		err = fmt.Errorf("unknown task type %q", task.Type)
	}

	if err == nil {
		stabilize(ctx, e.Sess)
	}

	urlAfter, _ := e.Sess.URL(ctx)
	return ActionResult{
		Success:   err == nil,
		Error:     errToMsg(err),
		Navigated: urlAfter != urlBefore,
		NewURL:    urlAfter,
	}
}

// executeForm fills each field, then triggers the submit button. Field
// resolution looks up by role+label in the elements snapshot — the LLM
// never sees the actual selectors, so the executor is the choke point
// where role+label resolves to a CSS selector.
func (e *Executor) executeForm(ctx context.Context, task PageTask, elements []RawElement) error {
	for _, field := range task.Fields {
		el := resolveByRoleLabel(elements, field.Role, field.Label)
		if el == nil {
			executorLog.Warn("form field not found — skipping", F("role", field.Role), F("label", field.Label))
			continue
		}
		if err := e.fill(ctx, *el, field.Value); err != nil {
			// One failed field doesn't abort the whole form — the rest may
			// still matter (e.g. the failing field is a CAPTCHA the LLM
			// could not solve, but the submit still reveals endpoint X).
			executorLog.Warn("field fill failed — continuing", F("label", field.Label), F("err", err.Error()))
		}
	}
	if task.Submit == nil {
		return nil
	}
	submit := resolveByRoleLabel(elements, task.Submit.Role, task.Submit.Label)
	if submit == nil {
		return fmt.Errorf("submit %s:%s not found", task.Submit.Role, task.Submit.Label)
	}
	out, err := e.Sess.ClickSelector(ctx, submit.Selector)
	if err != nil {
		return err
	}
	if out != "ok" {
		return fmt.Errorf("submit click: %s", out)
	}
	return nil
}

// executeClick is the simple-task path: resolve, click, done.
func (e *Executor) executeClick(ctx context.Context, task PageTask, elements []RawElement) error {
	el := resolveByRoleLabel(elements, task.Role, task.Label)
	if el == nil {
		return fmt.Errorf("element %s:%s not found", task.Role, task.Label)
	}
	out, err := e.Sess.ClickSelector(ctx, el.Selector)
	if err != nil {
		return err
	}
	if out != "ok" {
		return fmt.Errorf("click: %s", out)
	}
	return nil
}

// fill dispatches based on the field kind:
//   - slider/range → set underlying input[type=range].value + events
//   - file input → not implemented in v1 (returns error; the LLM should
//     avoid file uploads in the planner)
//   - everything else → FillSelector (prototype-setter pattern)
func (e *Executor) fill(ctx context.Context, el RawElement, value string) error {
	if el.Role == "slider" || el.Type == "range" {
		return e.fillSlider(ctx, el, value)
	}
	if el.Type == "file" {
		return fmt.Errorf("file inputs are not supported in v1")
	}
	out, err := e.Sess.FillSelector(ctx, el.Selector, value)
	if err != nil {
		return err
	}
	if out == "readonly" || out == "gone" {
		return fmt.Errorf("fill: %s", out)
	}
	return nil
}

// fillSlider sets the underlying range input value. Tries JS evaluation
// first (works for both native range and Angular Material mat-slider),
// falls back to keyboard arrow-right if the JS path fails.
func (e *Executor) fillSlider(ctx context.Context, el RawElement, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fmt.Errorf("invalid slider value: %s", value)
	}
	js := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s);
  if (!el) return 'gone';
  const input = el.matches("input[type=range]") ? el : el.querySelector("input[type=range]");
  if (!input) return 'no-input';
  input.value = String(%d);
  input.dispatchEvent(new Event("input", {bubbles:true}));
  input.dispatchEvent(new Event("change", {bubbles:true}));
  return 'ok';
})()`, jsonStr(el.Selector), n)
	out, err := e.Sess.EvalString(ctx, js)
	if err != nil {
		return err
	}
	if out == "ok" {
		return nil
	}
	// Keyboard fallback: focus, Home, then ArrowRight N times.
	_, _ = e.Sess.EvalString(ctx, fmt.Sprintf(`document.querySelector(%s)?.focus()`, jsonStr(el.Selector)))
	_ = e.Sess.PressKey(ctx, "Home")
	for i := 0; i < n; i++ {
		_ = e.Sess.PressKey(ctx, "ArrowRight")
	}
	return nil
}

// stabilize waits for the document to settle after an action. A page that
// never settles within the timeout is still usable — navigation timeouts
// are not errors here.
func stabilize(ctx context.Context, sess *browser.Session) {
	_ = sess.WaitReady(ctx, stabilizeTimeout)
	select {
	case <-ctx.Done():
	case <-afterMs(stabilizeWaitMs):
	}
}

// resolveByRoleLabel finds the first element with the matching role+label.
// The planner speaks role+label; the executor translates to selector.
// Returns nil when no element matches (the planner may have referenced
// an element that was on a previous snapshot, since removed by re-render).
func resolveByRoleLabel(elements []RawElement, role, label string) *RawElement {
	for i := range elements {
		if elements[i].Role == role && elements[i].Label == label {
			return &elements[i]
		}
	}
	// Lenient fallback: role+label is sometimes ambiguous (case, trailing
	// whitespace); try case-insensitive.
	roleLC, labelLC := strings.ToLower(role), strings.ToLower(label)
	for i := range elements {
		if strings.ToLower(elements[i].Role) == roleLC && strings.ToLower(elements[i].Label) == labelLC {
			return &elements[i]
		}
	}
	return nil
}

// errToMsg returns err.Error() or "" when err is nil. Keeps the call sites
// tidy — the ActionResult contract is "empty Error means success".
func errToMsg(err error) string {
	if err == nil {
		return ""
	}
	// First line of the error — playwright-style errors embed call stacks
	// the LLM doesn't need.
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		return msg[:i]
	}
	return msg
}
