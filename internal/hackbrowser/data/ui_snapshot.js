// ui_snapshot.js — page-side form field collection, runs inside the browser.
//
// Ported from packages/hackbrowser/src/capture.ts (snapshotPageUI's
// page.evaluate body). Strips TypeScript types; behaviour is identical.
//
// The wrapper is provided by the Go caller:
//   (function(triggerSel){ <BODY> })("<css-selector>")
//
// Returns a JSON array of UIField-shaped objects.

// Kural 3: modal/dialog container selectors.
const DIALOG_SEL = "[role=dialog], [role=alertdialog], [aria-modal='true'], mat-dialog-container, .cdk-overlay-pane";

const results = [];

function getLabel(el) {
  const aria = el.getAttribute("aria-label");
  if (aria) return aria.trim();
  const id = el.getAttribute("id");
  if (id) {
    const label = document.querySelector('label[for="' + CSS.escape(id) + '"]');
    if (label) return (label.textContent || "").trim();
  }
  const parentLabel = el.closest("label");
  if (parentLabel) return (parentLabel.textContent || "").trim();
  const ph = el.placeholder;
  if (ph) return ph;
  return el.name || el.getAttribute("data-name") || "";
}

function computeHidden(el, type) {
  if (type === "hidden") return { isHidden: true, hiddenReason: "type=hidden" };
  let cur = el;
  while (cur && cur !== document.documentElement) {
    const style = window.getComputedStyle(cur);
    if (style.display === "none") return { isHidden: true, hiddenReason: "display:none" };
    if (style.visibility === "hidden") return { isHidden: true, hiddenReason: "visibility:hidden" };
    if (parseFloat(style.opacity) === 0) return { isHidden: true, hiddenReason: "opacity:0" };
    cur = cur.parentElement;
  }
  return { isHidden: false };
}

function isHiddenCSS(el) {
  return computeHidden(el, "").isHidden;
}

const NOISE_ID_PREFIX = /^(cdk-|mat-)/i;

// Resolve scope: trigger → form → dialog → none; empty trigger → document.
let root;
if (triggerSel) {
  const triggerEl = document.querySelector(triggerSel);
  if (triggerEl) {
    root = triggerEl.closest("form") || triggerEl.closest(DIALOG_SEL);
  } else {
    root = null;
  }
  if (!root) return [];
} else {
  root = document;
}

const inputs = root.querySelectorAll("input, textarea, select");

const radioGroups = new Map();

for (const el of inputs) {
  const tagName = el.tagName.toLowerCase();
  const type = tagName === "input" ? (el.type || "").toLowerCase() : tagName;

  const name =
    el.getAttribute("name") ||
    el.getAttribute("id") ||
    el.getAttribute("data-name") ||
    el.getAttribute("data-testid") ||
    el.getAttribute("data-test") ||
    el.getAttribute("data-cy") ||
    el.getAttribute("aria-label") ||
    "";

  if (NOISE_ID_PREFIX.test(name) && el.disabled) continue;

  // Collapse radios by name.
  if (type === "radio") {
    const radioName = el.getAttribute("name") || "";
    if (!radioName) continue;
    const existing = radioGroups.get(radioName);
    if (existing) {
      existing.allValues.push(el.value);
      if (el.checked) existing.checkedValue = el.value;
      if (el.required) existing.anyRequired = true;
      if (!el.disabled) existing.allDisabled = false;
      if (isHiddenCSS(el)) existing.anyCSSHidden = true;
    } else {
      radioGroups.set(radioName, {
        name: radioName,
        label: getLabel(el),
        checkedValue: el.checked ? el.value : "",
        allValues: [el.value],
        anyRequired: el.required,
        allDisabled: el.disabled,
        anyCSSHidden: isHiddenCSS(el)
      });
    }
    continue;
  }

  let value;
  if (type === "checkbox") value = String(el.checked);
  else if (type === "select" || tagName === "select") value = el.value;
  else value = el.value || "";

  const hidden = computeHidden(el, type);

  results.push({
    name: name,
    label: getLabel(el),
    value: value,
    type: type,
    isReadOnly: !!el.readOnly,
    isDisabled: !!el.disabled,
    isHidden: hidden.isHidden,
    hiddenReason: hidden.hiddenReason || "",
    isDisplayOnly: false,
    validation: {
      min: el.min || "",
      max: el.max || "",
      maxLength: el.maxLength > 0 ? String(el.maxLength) : "",
      pattern: el.pattern || "",
      required: !!el.required
    }
  });
}

// Emit one UIField per radio group.
for (const group of radioGroups.values()) {
  const first3 = group.allValues.slice(0, 3).join(", ");
  const options = group.allValues.length > 3 ? first3 + ", ..." : first3;
  results.push({
    name: group.name,
    label: group.label,
    value: group.checkedValue,
    type: "radio",
    options: options,
    isReadOnly: false,
    isDisabled: group.allDisabled,
    isHidden: group.anyCSSHidden,
    hiddenReason: "",
    isDisplayOnly: false,
    validation: { required: group.anyRequired }
  });
}

// Display-only values (data-* attributed elements, not generic spans).
const displaySelectors = [
  "[data-field]",
  "[data-value]",
  "[data-id]",
  ".field-value",
  ".read-only-value",
  ".display-value",
  "td[id]"
];

for (const sel of displaySelectors) {
  const els = root.querySelectorAll(sel);
  for (const el of els) {
    if (el.querySelector("input, select, textarea, button")) continue;
    if (el.matches("[role=option], [role=menuitem], [role=tab]")) continue;
    if (el.closest("[role=listbox], [role=menu], [role=combobox]")) continue;
    const name = el.getAttribute("data-field") || el.getAttribute("data-name") || el.getAttribute("id") || "";
    if (NOISE_ID_PREFIX.test(name)) continue;
    const text = (el.textContent || "").trim();
    if (!text) continue;
    results.push({
      name: name,
      label: el.getAttribute("aria-label") || name,
      value: text,
      type: "display",
      isReadOnly: true,
      isDisabled: false,
      isHidden: isHiddenCSS(el),
      hiddenReason: "",
      isDisplayOnly: true,
      validation: {}
    });
  }
}

return results;
