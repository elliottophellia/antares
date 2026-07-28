// Scanner: collect every interactive element on the current page.
//
// The collection logic runs inside the browser (data/scanner.js) via CDP
// Runtime.evaluate — it walks the DOM, pierces open shadow roots, dedups
// same-label siblings, and produces a structured array of every control a
// person could act on. The Go side parses that array, assigns E1/E2/...
// identifiers, applies the template-sampling and element-cap policies, and
// returns []RawElement ready for the planner.
//
// The auxiliary reveal/expand/occlusion probes are smaller JS snippets
// inlined here — they each run as a single expression.

package hackbrowser

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/browser"
)

//go:embed data/scanner.js
var scannerJS string

const (
	maxElements     = 50
	maxPerTemplate  = 5
	revealMaxSteps  = 10
	revealStepWaitMs = 250
	expandMax       = 20
)

// rawScannerElement mirrors the shape returned by data/scanner.js. The
// fields map 1:1 onto RawElement except for selectorRole/selectorCSS which
// collapse into RawElement.Selector at assign time.
type rawScannerElement struct {
	Tag           string `json:"tag"`
	Role          string `json:"role"`
	Label         string `json:"label"`
	Value         string `json:"value"`
	Enabled       bool   `json:"enabled"`
	Href          string `json:"href"`
	Type          string `json:"type"`
	Placeholder   string `json:"placeholder"`
	Options       string `json:"options"`
	Constraints   string `json:"constraints"`
	SelectorRole  string `json:"selectorRole"`
	SelectorCSS   string `json:"selectorCSS"`
	InChrome      bool   `json:"inChrome"`
}

// CollectElements runs the scanner in the page and returns the prepared
// element list. Pure — no caching, no mutations outside the page.
func CollectElements(ctx context.Context, sess *browser.Session) ([]RawElement, error) {
	raw, err := sess.Eval(ctx, scannerJS)
	if err != nil {
		return nil, fmt.Errorf("scanner: %w", err)
	}
	var scanned []rawScannerElement
	if err := json.Unmarshal(raw, &scanned); err != nil {
		return nil, fmt.Errorf("scanner: invalid response: %w", err)
	}
	sampled := sampleTemplates(scanned)
	capped := capElements(sampled)
	return assignIDs(capped, 1), nil
}

// assignIDs converts the scanner's raw output to RawElement with stable E*
// identifiers, picking the best available selector for each element.
func assignIDs(els []rawScannerElement, startID int) []RawElement {
	out := make([]RawElement, 0, len(els))
	for i, e := range els {
		selector := e.SelectorCSS
		// Prefer a role+name selector when unique; the collector already
		// blanks selectorRole for ambiguous cases (seenRoleSelectors > 1).
		if strings.Contains(e.SelectorRole, "[name=") {
			selector = e.SelectorRole
		} else if selector == "" {
			selector = e.SelectorRole
		}
		out = append(out, RawElement{
			ID:          fmt.Sprintf("E%d", startID+i),
			Tag:         e.Tag,
			Role:        e.Role,
			Label:       e.Label,
			Value:       e.Value,
			Enabled:     e.Enabled,
			Href:        e.Href,
			Type:        e.Type,
			Placeholder: e.Placeholder,
			Options:     e.Options,
			Constraints: e.Constraints,
			Selector:    selector,
			InChrome:    e.InChrome,
		})
	}
	return out
}

// sampleTemplates collapses templated (numbered) sibling clusters to a
// handful of representatives before the global cap kicks in, so a long
// "Item 1..55" list cannot crowd out unique, security-critical controls.
//
// Clustering is digit-masked: "Item 1"/"Item 2" → "Item #" cluster; a label
// with NO digits ("Delete Account", "Delete User") masks to itself and
// stays a singleton. The first maxPerTemplate of each cluster survive.
func sampleTemplates(elements []rawScannerElement) []rawScannerElement {
	clusterCount := map[string]int{}
	out := make([]rawScannerElement, 0, len(elements))
	for _, el := range elements {
		masked := digitRE.ReplaceAllString(el.Label, "#")
		if masked == el.Label {
			out = append(out, el) // no digits → unique
			continue
		}
		key := el.Role + "::" + masked
		n := clusterCount[key] + 1
		clusterCount[key] = n
		if n <= maxPerTemplate {
			out = append(out, el)
		}
	}
	return out
}

var digitRE = regexp.MustCompile(`\d+`)

// capElements bounds the element list to maxElements while preferring
// action roles (button/link/menuitem/tab). Losing an input costs one field;
// losing the submit button breaks the whole form, so the primary actions
// are protected.
func capElements(els []rawScannerElement) []rawScannerElement {
	if len(els) <= maxElements {
		return els
	}
	actionSet := map[string]bool{"button": true, "link": true, "menuitem": true, "tab": true}

	// Pick indices to keep: all actions first, then fill from the rest in
	// original order until we hit maxElements. This is O(N) and avoids the
	// pointer-comparison trap the previous implementation fell into.
	keep := make([]bool, len(els))
	actionCount := 0
	for i, e := range els {
		if actionSet[e.Role] {
			keep[i] = true
			actionCount++
		}
	}
	if actionCount > maxElements {
		// More actions than the cap — keep only the first maxElements.
		kept := 0
		for i := range els {
			if keep[i] {
				if kept >= maxElements {
					keep[i] = false
				} else {
					kept++
				}
			}
		}
	} else {
		// Fill the remaining budget with non-actions in DOM order.
		remaining := maxElements - actionCount
		for i, e := range els {
			if remaining <= 0 {
				break
			}
			if !actionSet[e.Role] {
				keep[i] = true
				remaining--
			}
		}
	}

	out := make([]rawScannerElement, 0, maxElements)
	for i := range els {
		if keep[i] {
			out = append(out, els[i])
		}
	}
	return out
}

// ============================================================
// Page preparation: reveal lazy content, expand disclosures
// ============================================================

// RevealLazyContent scrolls the page down in viewport-sized steps to trip
// IntersectionObserver-based lazy loading, then returns to the top. Bounded
// so an infinite-scroll page cannot trap the crawl. Non-fatal on mid-scroll
// navigation.
func RevealLazyContent(ctx context.Context, sess *browser.Session) {
	const stepJS = `(() => {
  const before = window.scrollY;
  window.scrollBy(0, Math.round(window.innerHeight * 0.9));
  return window.scrollY > before;
})()`
	for i := 0; i < revealMaxSteps; i++ {
		raw, err := sess.Eval(ctx, stepJS)
		if err != nil {
			return // navigation destroyed context — non-fatal
		}
		var advanced bool
		_ = json.Unmarshal(raw, &advanced)
		if !advanced {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-afterMs(revealStepWaitMs):
		}
	}
	_, _ = sess.Eval(ctx, "window.scrollTo(0, 0)")
	select {
	case <-ctx.Done():
	case <-afterMs(revealStepWaitMs):
	}
}

// ExpandDisclosures opens safe-by-ARIA-semantics collapsible regions so
// their hidden controls become real attack surface in the snapshot, without
// spending LLM turns on them. Two tiers:
//   - native <details> → set .open (no event, zero side effect)
//   - [aria-expanded="false"] controls without aria-haspopup → click
//
// Tabs (role=tab) are excluded (selecting one mutates active-panel state).
// Bounded by expandMax. Non-fatal on mid-expand navigation.
func ExpandDisclosures(ctx context.Context, sess *browser.Session) {
	js := fmt.Sprintf(`(() => {
  let budget = %d;
  for (const d of Array.from(document.querySelectorAll("details:not([open])"))) {
    if (budget <= 0) break;
    if (d.closest("[data-cyberstrike-ui]")) continue;
    d.open = true;
    budget--;
  }
  for (const c of Array.from(document.querySelectorAll('[aria-expanded="false"]'))) {
    if (budget <= 0) break;
    if (c.closest("[data-cyberstrike-ui]")) continue;
    if (c.getAttribute("role") === "tab") continue;
    if (c.hasAttribute("aria-haspopup") && c.getAttribute("aria-haspopup") !== "false") continue;
    c.click();
    budget--;
  }
})()`, expandMax)
	_, _ = sess.Eval(ctx, js)
	select {
	case <-ctx.Done():
	case <-afterMs(revealStepWaitMs):
	}
}

// ============================================================
// Occlusion probes
// ============================================================

// IsViewportCenterBlocked reports whether a modal/backdrop overlay is
// covering the viewport center — the planner uses this to decide whether
// to plan a "dismiss" task before anything else.
func IsViewportCenterBlocked(ctx context.Context, sess *browser.Session) bool {
	out, err := blockedEval(ctx, sess)
	if err != nil {
		return false
	}
	return out
}

const viewportBlockedJS = `(() => {
  const cx = window.innerWidth / 2;
  const cy = window.innerHeight / 2;
  const el = document.elementFromPoint(cx, cy);
  if (!el) return false;
  const tag = el.tagName.toLowerCase();
  const role = el.getAttribute("role") || "";
  const cls = el.className || "";
  const id = el.getAttribute("id") || "";
  if (/backdrop|overlay|cdk-overlay|modal-backdrop/i.test(String(cls))) return true;
  if (/backdrop|overlay/i.test(id)) return true;
  if (role === "dialog" || role === "alertdialog") return true;
  if (tag === "mat-dialog-container") return true;
  const style = window.getComputedStyle(el);
  if (style.position === "fixed" && parseFloat(style.opacity) > 0) {
    const rect = el.getBoundingClientRect();
    if (rect.width >= window.innerWidth * 0.9 && rect.height >= window.innerHeight * 0.9) {
      const bg = style.backgroundColor;
      if (bg && bg.startsWith("rgba") && !bg.endsWith(", 0)")) return true;
    }
  }
  let ancestor = el.parentElement;
  while (ancestor && ancestor !== document.documentElement) {
    const aCls = typeof ancestor.className === "string" ? ancestor.className : "";
    const aId = ancestor.getAttribute("id") || "";
    const aRole = ancestor.getAttribute("role") || "";
    if (/modal|dialog|overlay|backdrop/i.test(aCls)) return true;
    if (/modal|dialog|overlay|backdrop/i.test(aId)) return true;
    if (aRole === "dialog" || aRole === "alertdialog") return true;
    ancestor = ancestor.parentElement;
  }
  const candidates = document.querySelectorAll(
    '[class*="overlay"],[class*="backdrop"],[class*="modal"],[role="dialog"],[role="alertdialog"]'
  );
  for (const c of candidates) {
    const s = window.getComputedStyle(c);
    if (s.display === "none" || s.visibility === "hidden") continue;
    if (parseFloat(s.opacity) === 0) continue;
    const r = c.getBoundingClientRect();
    if (r.width >= window.innerWidth * 0.8 && r.height >= window.innerHeight * 0.8) return true;
  }
  return false;
})()`

func blockedEval(ctx context.Context, sess *browser.Session) (bool, error) {
	raw, err := sess.Eval(ctx, viewportBlockedJS)
	if err != nil {
		// Navigation destroyed context — treat as not blocked so the loop
		// can continue onto whatever the new page is.
		return false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, err
	}
	return b, nil
}

// ClickPointProbe reports whether an element matching sel can be clicked at
// its centre, or whether an overlay occludes it. Status is one of:
//   - "clickable" — proceed normally
//   - "offscreen"  — needs a scroll first
//   - "occluded"   — an unrelated element is on top; the planner should
//                    plan a dismiss action first
//
// The probe is best-effort: any failure to resolve the element returns
// "clickable" so the normal click path runs unchanged.
type ClickPointProbe struct {
	Status   string
	Occluder *OccluderInfo
}

// OccluderInfo describes the element covering a click target.
type OccluderInfo struct {
	Tag   string `json:"tag"`
	Role  string `json:"role"`
	Text  string `json:"text"`
}

// ProbeClickPoint runs the occlusion probe for one selector.
func ProbeClickPoint(ctx context.Context, sess *browser.Session, selector string) ClickPointProbe {
	js := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s);
  if (!el) return {status:"clickable"};
  const r = el.getBoundingClientRect();
  if (r.width === 0 || r.height === 0) return {status:"clickable"};
  const cx = r.left + r.width / 2;
  const cy = r.top + r.height / 2;
  if (cx < 0 || cy < 0 || cx > window.innerWidth || cy > window.innerHeight) {
    return {status:"offscreen"};
  }
  const top = document.elementFromPoint(cx, cy);
  if (!top) return {status:"offscreen"};
  if (top === el || el.contains(top) || top.contains(el)) return {status:"clickable"};
  if (top.tagName === "IFRAME") return {status:"clickable"};
  let node = el;
  while (node) {
    const root = node.getRootNode();
    if (root instanceof ShadowRoot) {
      const host = root.host;
      if (host === top || top.contains(host)) return {status:"clickable"};
      node = host;
    } else break;
  }
  const text = ((top.innerText || "") + " " + (top.getAttribute("aria-label") || "")).trim().replace(/\s+/g, " ").slice(0, 80);
  return {
    status: "occluded",
    occluder: { tag: top.tagName.toLowerCase(), role: top.getAttribute("role") || "", text: text }
  };
})()`, jsonStr(selector))
	raw, err := sess.Eval(ctx, js)
	if err != nil {
		return ClickPointProbe{Status: "clickable"}
	}
	var probe struct {
		Status   string        `json:"status"`
		Occluder *OccluderInfo `json:"occluder"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ClickPointProbe{Status: "clickable"}
	}
	return ClickPointProbe{Status: probe.Status, Occluder: probe.Occluder}
}

// ============================================================
// Pure helpers
// ============================================================

// FilterVisitedLinks drops links whose target points at a page that has
// already been visited (or is the current page). Non-link elements and
// unparseable hrefs are always kept.
func FilterVisitedLinks(elements []RawElement, currentURL string, visited map[string]bool) []RawElement {
	currentPath := currentURL
	if u, err := parseURL(currentURL); err == nil {
		currentPath = u.Path + "#" + u.Fragment
	}
	out := make([]RawElement, 0, len(elements))
	for _, el := range elements {
		if el.Role != "link" || el.Href == "" {
			out = append(out, el)
			continue
		}
		u, err := parseURL(el.Href)
		if err != nil {
			out = append(out, el)
			continue
		}
		path := u.Path + "#" + u.Fragment
		if path == currentPath {
			continue
		}
		if visited[el.Href] || visited[NormalizeURL(el.Href)] {
			continue
		}
		out = append(out, el)
	}
	return out
}

// afterMs returns a channel that fires after the given number of
// milliseconds. Used to drive reveal/expand waits without pulling in time.
func afterMs(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}

// jsonStr quotes s as a JavaScript string literal (JSON string syntax).
func jsonStr(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// parseURL wraps net/url.Parse so call sites can dismiss the error in one
// line; malformed hrefs fall through to "keep the element" policies.
func parseURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}
