// Capture: snapshot the page's UI state for correlation with the HTTP
// requests the action fired, and build the wire-format raw request string
// the rest of the engagement analyses.
//
// Three responsibilities:
//   1. snapshotPageUI  — collect form fields visible around a trigger element
//   2. buildRawRequest — wire-format a request as "METHOD /path HTTP/1.1\r\n..."
//   3. correlateWithUI — flag params present in the request but missing from UI

package hackbrowser

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/enowdev/antares/internal/browser"
)

//go:embed data/ui_snapshot.js
var uiSnapshotJS string

// SnapshotPageUI collects form fields visible on the page, scoped to the
// form or dialog that contains the trigger element. The returned UIContext
// is what gets attached to HTTP captures for later correlation.
//
// triggerSel is a CSS selector for the element that triggered the action
// (typically the submit button); pass empty for a full-page scan. The
// componentPath is cosmetic — used as a hint in the captured UIContext.
func SnapshotPageUI(
	ctx context.Context,
	sess *browser.Session,
	triggerSel, componentPath string,
) (*UIContext, error) {
	pageURL, _ := sess.URL(ctx)
	pageTitle, _ := sess.Title(ctx)

	// Resolve the trigger selector to a DOM handle inside the page
	// evaluate — Playwright passes an ElementHandle across the IPC
	// boundary, but we can short-circuit that by passing the selector
	// string and resolving it in JS.
	body := "(function(triggerSel){" + extractFuncBody(uiSnapshotJS) + "})(" + jsonStr(triggerSel) + ")"
	raw, err := sess.Eval(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("ui snapshot: %w", err)
	}
	var fields []UIField
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("ui snapshot parse: %w", err)
	}

	// Form name best-guess.
	formName, _ := sess.EvalString(ctx, `(() => {
  const form = document.querySelector("form");
  if (form) return form.getAttribute("aria-label") || form.id || form.name || "";
  const h = document.querySelector("h1, h2, [role=heading]");
  return h ? (h.textContent || "").trim() : "";
})()`)

	path := componentPath
	if path == "" {
		path = pageTitle
	}
	return &UIContext{
		PageURL:       pageURL,
		PageTitle:     pageTitle,
		ComponentPath: path,
		FormName:      formName,
		Fields:        fields,
	}, nil
}

// extractFuncBody strips a leading "function ...{" and trailing "}" from a
// JS source string so the body can be inlined into a fresh wrapper. The
// ui_snapshot.js file is written as a function expression returning the
// fields array; we wrap it in (function(triggerSel){ ... })(sel) for Eval.
func extractFuncBody(src string) string {
	s := strings.TrimSpace(src)
	// Strip a leading "function (...) {" or "(() =>" form; we just want the body.
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "}"); i >= 0 {
		s = s[:i]
	}
	return s
}

// ============================================================
// Wire-format raw request
// ============================================================

// BuildRawRequest formats an HTTP request as the raw wire string the rest
// of the engagement expects:
//
//	POST /api/Users?x=1 HTTP/1.1
//	Host: example.com
//	Content-Type: application/json
//
//	{"name":"alice"}
//
// Mirrors what a browser extension captures, so downstream tooling does not
// have to know it came from a headless crawler.
func BuildRawRequest(method, rawURL string, headers map[string]string, body string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		// Fall back to a path-only form so the caller still gets something.
		return fmt.Sprintf("%s %s HTTP/1.1\r\n\r\n%s", method, rawURL, body)
	}
	path := u.Path
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		path += "#" + u.Fragment
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", method, path)

	hasHost := false
	// Stable header order: sort keys for deterministic output.
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	// Simple insertion sort — header maps are small.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\r\n", k, headers[k])
		if strings.EqualFold(k, "host") {
			hasHost = true
		}
	}
	if !hasHost {
		fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	}
	if body != "" {
		fmt.Fprintf(&b, "\r\n%s", body)
	}
	return b.String()
}

// ============================================================
// Request ↔ UI correlation
// ============================================================

// CorrelateWithUI flags request parameters that are NOT present in the UI
// snapshot — hidden fields the page did not surface to the user. These are
// candidates for tampering attacks (CSRF tokens, legacy ids, debug flags).
//
// The match is case-insensitive on field name. requestParams is the flat
// key=value map parsed from the request body or query string.
func CorrelateWithUI(ui *UIContext, requestParams map[string]string) *UIContext {
	if ui == nil {
		return nil
	}
	uiFieldNames := map[string]bool{}
	for _, f := range ui.Fields {
		uiFieldNames[strings.ToLower(f.Name)] = true
	}
	var hidden []string
	for k := range requestParams {
		if !uiFieldNames[strings.ToLower(k)] {
			hidden = append(hidden, k)
		}
	}
	cp := *ui
	cp.HiddenParams = hidden
	return &cp
}

// ParseRequestParams flattens a request's query string and body into a
// flat key=value map. Tries JSON first (dot-path flattening), then
// form-encoding. Always also parses the URL query string.
func ParseRequestParams(body, rawURL string) map[string]string {
	out := map[string]string{}

	// Query string.
	if u, err := url.Parse(rawURL); err == nil {
		for k, vs := range u.Query() {
			if len(vs) > 0 {
				out[k] = vs[0]
			}
		}
	}

	if body == "" {
		return out
	}

	// JSON body.
	trimmed := strings.TrimSpace(body)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		var any interface{}
		if err := json.Unmarshal([]byte(trimmed), &any); err == nil {
			if m, ok := any.(map[string]interface{}); ok {
				flattenObject(m, "", out)
				return out
			}
			// Arrays and other top-level shapes: don't flatten, the caller
			// has the raw body anyway.
		}
	}

	// Form-encoded body.
	if vs, err := url.ParseQuery(body); err == nil {
		for k, vals := range vs {
			if len(vals) > 0 {
				out[k] = vals[0]
			}
		}
	}

	return out
}

// flattenObject walks a nested JSON object, producing dot-path keys:
// {"user":{"name":"alice"}} → {"user.name":"alice"}.
func flattenObject(obj map[string]interface{}, prefix string, out map[string]string) {
	for k, v := range obj {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			flattenObject(val, key, out)
		case []interface{}:
			// Arrays: keep the first element as a string for the simple
			// case (multi-value selects, tag lists). The full array stays
			// in the raw body for analysts.
			if len(val) > 0 {
				out[key] = fmt.Sprintf("%v", val[0])
			}
		default:
			if v != nil {
				out[key] = fmt.Sprintf("%v", v)
			}
		}
	}
}
