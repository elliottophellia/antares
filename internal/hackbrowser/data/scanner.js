// scanner.js — page-side DOM collection, runs inside the browser via
// Runtime.evaluate. Returns a JSON array of rawScannerElement objects.
//
// Ported verbatim from packages/hackbrowser/src/scanner.ts (the body of
// collectInteractiveElements' page.evaluate). The TypeScript types are
// stripped; the runtime behaviour is identical.
//
// This file is embedded into the binary at build time and shipped to the
// page as one expression: it MUST NOT have side effects on import — the
// outer IIFE wrapper is added by the Go caller.
//
// Returned object shape (one element):
//   { tag, role, label, value, enabled, href, type, placeholder,
//     options, constraints, selectorRole, selectorCSS, inChrome }

(() => {
  const FRAMEWORK_CLICK_ATTRS = [
    "wire:click",
    "hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete",
    "ng-click",
    "x-on:click", "@click",
    "data-toggle", "data-bs-toggle",
    "data-hx-get", "data-hx-post", "data-hx-put", "data-hx-patch", "data-hx-delete",
    "data-ng-click"
  ];

  function isStructurallyVisible(el) {
    const rect = el.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) return false;
    const style = window.getComputedStyle(el);
    if (style.display === "none") return false;
    if (style.visibility === "hidden") return false;
    const tag = el.tagName.toLowerCase();
    const isFormControl = tag === "input" || tag === "select" || tag === "textarea";
    if (parseFloat(style.opacity) === 0 && !isFormControl) return false;
    if (el.getAttribute("aria-hidden") === "true") return false;
    if (style.pointerEvents === "none" && !el.disabled) return false;
    return true;
  }

  function getLabel(el) {
    const ariaLabel = (el.getAttribute("aria-label") || "").trim();
    if (ariaLabel) {
      const childText = (el.innerText || "").trim();
      if (childText && childText.length > 5 && childText.length < 80 && childText !== ariaLabel) {
        return ariaLabel + " — " + childText;
      }
      return ariaLabel;
    }
    const ariaLabelledBy = el.getAttribute("aria-labelledby");
    if (ariaLabelledBy) {
      const labelEl = document.getElementById(ariaLabelledBy);
      if (labelEl && labelEl.textContent && labelEl.textContent.trim()) return labelEl.textContent.trim();
    }
    const id = el.getAttribute("id");
    if (id) {
      const labelEl = document.querySelector('label[for="' + CSS.escape(id) + '"]');
      if (labelEl && labelEl.textContent && labelEl.textContent.trim()) return labelEl.textContent.trim();
    }
    const text = (el.innerText || "").trim();
    if (text && text.length < 80) return text;
    const parentLabel = el.closest ? el.closest("label") : null;
    if (parentLabel && !parentLabel.isSameNode(el)) {
      const parentText = (parentLabel.textContent || "").trim();
      if (parentText && parentText.length < 80) return parentText;
    }
    const placeholder = el.placeholder;
    if (placeholder) return placeholder;
    if (el.tagName.toLowerCase() === "input") {
      const itype = (el.type || "").toLowerCase();
      if (itype === "image") {
        const alt = (el.getAttribute("alt") || "").trim();
        if (alt) return alt;
      }
      if (itype === "submit" || itype === "button" || itype === "image") {
        const val = (el.value || "").trim();
        if (val) return val;
      }
    }
    const root = el.getRootNode();
    if (root instanceof ShadowRoot && root.host) {
      const host = root.host;
      const hostAria = (host.getAttribute("aria-label") || "").trim();
      if (hostAria) return hostAria;
      const hostLabelAttr = (host.getAttribute("label") || "").trim();
      if (hostLabelAttr) return hostLabelAttr;
      const hostText = (host.innerText || "").trim();
      if (hostText && hostText.length < 80) return hostText;
    }
    const name = el.getAttribute("name") || el.getAttribute("data-testid");
    if (name) return name;
    return "";
  }

  function getRole(el) {
    const explicit = el.getAttribute("role");
    if (explicit) return explicit.toLowerCase();
    const tag = el.tagName.toLowerCase();
    const type = (el.type || "").toLowerCase();
    if (tag === "button") return "button";
    if (tag === "summary") return "button";
    if (tag === "a" && el.getAttribute("href")) return "link";
    if (tag === "input") {
      if (type === "submit" || type === "button" || type === "image") return "button";
      if (type === "checkbox") return "checkbox";
      if (type === "radio") return "radio";
      if (type === "hidden") return "";
      if (type === "range") return "slider";
      return "textbox";
    }
    if (tag === "textarea") return "textbox";
    if (tag === "select") return "combobox";
    if (tag === "li" && el.closest("[role=menu],[role=listbox]")) return "menuitem";
    if (el.hasAttribute("onclick")) return "button";
    for (const a of FRAMEWORK_CLICK_ATTRS) {
      if (el.hasAttribute(a)) return "button";
    }
    return "";
  }

  function isEphemeralId(id) {
    if (!id) return false;
    if (/^:/.test(id) || /:r[0-9a-z]+:/i.test(id)) return true;
    return /^(mui-\d|radix-|headlessui-|react-aria-|rc-_?\d|ember\d)/i.test(id);
  }

  function ancestorPrefix(el) {
    let current = el.parentElement;
    while (current && current !== document.documentElement) {
      const aTag = current.tagName.toLowerCase();
      const aId = current.getAttribute("id");
      if (aId && !isEphemeralId(aId)) return aTag + "#" + CSS.escape(aId) + " ";
      const aCls = typeof current.className === "string"
        ? current.className.trim().split(/\s+/).filter((c) => c.length > 2)[0]
        : undefined;
      if (aCls) return aTag + "." + CSS.escape(aCls) + " ";
      current = current.parentElement;
    }
    return "";
  }

  function buildCSSSelectorCandidate(el) {
    const tag = el.tagName.toLowerCase();
    const id = el.getAttribute("id");
    if (id && !isEphemeralId(id)) return tag + "#" + CSS.escape(id);
    const name = el.getAttribute("name");
    if (name) return tag + '[name="' + CSS.escape(name) + '"]';

    const cls = el.className;
    if (typeof cls === "string" && cls.trim()) {
      const classes = cls.trim().split(/\s+/).filter((c) => c.length > 2);
      if (classes.length > 0) {
        const clsSel = tag + "." + CSS.escape(classes[0]);
        const parent = el.parentElement;
        if (parent) {
          const siblings = Array.from(parent.querySelectorAll(":scope > " + clsSel));
          const idx = siblings.indexOf(el);
          if (idx >= 0) return ancestorPrefix(el) + clsSel + ":nth-of-type(" + (idx + 1) + ")";
        }
        return clsSel;
      }
    }
    const parent = el.parentElement;
    if (parent) {
      const siblings = Array.from(parent.querySelectorAll(":scope > " + tag));
      const idx = siblings.indexOf(el);
      if (idx >= 0) {
        const nthSel = tag + ":nth-of-type(" + (idx + 1) + ")";
        return ancestorPrefix(el) + nthSel;
      }
    }
    return tag;
  }

  function uniquePositionalPath(el) {
    const segs = [];
    let cur = el;
    while (cur && cur !== document.documentElement) {
      const tag = cur.tagName.toLowerCase();
      const id = cur.getAttribute("id");
      if (id && !isEphemeralId(id)) {
        segs.unshift(tag + "#" + CSS.escape(id));
        break;
      }
      const parent = cur.parentElement;
      if (!parent) {
        segs.unshift(tag);
        break;
      }
      const sameTag = Array.from(parent.children).filter((c) => c.tagName === cur.tagName);
      segs.unshift(sameTag.length > 1 ? tag + ":nth-of-type(" + (sameTag.indexOf(cur) + 1) + ")" : tag);
      cur = parent;
    }
    return segs.join(" > ");
  }

  function buildCSSSelector(el) {
    const candidate = buildCSSSelectorCandidate(el);
    try {
      if (document.querySelectorAll(candidate).length === 1) return candidate;
    } catch (e) {
      // malformed selector → fall through to positional path
    }
    return uniquePositionalPath(el);
  }

  const INTERACTIVE_SELECTORS = [
    "button",
    "a[href]",
    "input:not([type=hidden]):not([disabled])",
    "textarea:not([disabled])",
    "select:not([disabled])",
    "[role=button]",
    "[role=link]",
    "[role=menuitem]",
    "[role=tab]",
    "[role=checkbox]",
    "[role=radio]",
    "[role=combobox]",
    "[role=option]",
    "[role=slider]",
    "[onclick]",
    "summary"
  ].concat(FRAMEWORK_CLICK_ATTRS.map((a) => "[" + CSS.escape(a) + "]"))
   .join(", ");

  function queryAllDeep(selector) {
    const out = [];
    const walk = (root) => {
      for (const el of root.querySelectorAll(selector)) out.push(el);
      for (const host of root.querySelectorAll("*")) {
        if (host.closest("[data-cyberstrike-ui]")) continue;
        const sr = host.shadowRoot;
        if (sr) walk(sr);
      }
    };
    walk(document);
    return out;
  }

  function serializeConstraints(el, type) {
    const tag = el.tagName.toLowerCase();
    if (tag !== "input" && tag !== "textarea") return "";
    const parts = [];
    const getAttr = (name) => (el.getAttribute(name) || "").trim();

    const min = getAttr("min");
    const max = getAttr("max");
    const step = getAttr("step");
    const maxlength = getAttr("maxlength");
    const minlength = getAttr("minlength");
    const pattern = getAttr("pattern");

    const isNumericRange = type === "range" || type === "number";
    const isDateTime =
      type === "date" || type === "time" || type === "datetime-local" || type === "month" || type === "week";

    if (isNumericRange || isDateTime) {
      if (min) parts.push("min:" + min);
      if (max) parts.push("max:" + max);
      if (isNumericRange && step && step !== "any") parts.push("step:" + step);
    }
    if ((tag === "textarea" || ["text", "email", "url", "tel", "password", "search"].indexOf(type) >= 0) && maxlength) {
      parts.push("maxlength:" + maxlength);
    }
    if (minlength) parts.push("minlength:" + minlength);
    if (pattern) parts.push("pattern:" + pattern);
    if (["email", "url", "tel"].indexOf(type) >= 0) parts.push("type:" + type);

    return parts.join(" ");
  }

  const elements = [];
  const seenCount = new Map();
  const seenRoleSelectors = new Map();

  function addElement(el, role, syntheticRole) {
    syntheticRole = !!syntheticRole;
    const label = getLabel(el);
    const tag = el.tagName.toLowerCase();
    const type = (el.type || "").toLowerCase();
    const href = el.href || "";
    const isSlider = role === "slider";
    const value = isSlider
      ? (el.getAttribute("aria-valuenow") || el.value || "")
      : (el.value || "");
    const placeholder = el.placeholder || "";
    const enabled = !el.disabled;

    let options = "";
    if (tag === "select") {
      options = Array.from(el.querySelectorAll("option"))
        .map((o) => (o.textContent || "").trim())
        .filter(Boolean)
        .slice(0, 10)
        .join(", ");
    }

    const constraints = serializeConstraints(el, type);

    const innerText = (el.innerText || "").trim().slice(0, 40);
    const dedupKey = role + "::" + label + "::" + href + "::" + innerText;
    const count = (seenCount.get(dedupKey) || 0) + 1;
    seenCount.set(dedupKey, count);
    if (count > 3) return;
    const disambiguatedLabel = count > 1 ? label + " (" + count + ")" : label;

    const ariaLabelRaw = (el.getAttribute("aria-label") || "").trim();
    const safeAriaLabel = ariaLabelRaw.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
    const isFileInput = el.matches("input[type=file]");
    let selectorRole;
    if (syntheticRole || count > 1 || isFileInput) {
      selectorRole = "";
    } else if (safeAriaLabel) {
      selectorRole = 'role=' + role + '[name="' + safeAriaLabel + '"]';
    } else {
      selectorRole = "role=" + role;
    }
    const selectorCSS = buildCSSSelector(el);

    const inChrome = !!el.closest(
      "nav, header, footer, aside, [role=navigation], [role=banner], [role=contentinfo], [role=complementary]"
    );

    const roleCount = (seenRoleSelectors.get(selectorRole) || 0) + 1;
    seenRoleSelectors.set(selectorRole, roleCount);

    elements.push({
      tag: tag,
      role: role,
      label: disambiguatedLabel,
      value: value,
      enabled: enabled,
      href: href,
      type: type,
      placeholder: placeholder,
      options: options,
      constraints: constraints,
      selectorRole: selectorRole,
      selectorCSS: selectorCSS,
      inChrome: inChrome
    });
  }

  // ---- Interactive sweep ----
  for (const el of queryAllDeep(INTERACTIVE_SELECTORS)) {
    if (el.closest("[data-cyberstrike-ui]")) continue;
    const role = getRole(el);
    if (!role) continue;
    const isSlider = role === "slider";
    if (!isSlider && !isStructurallyVisible(el)) continue;
    addElement(el, role, false);
  }

  // ---- Heuristic clickable containers ----
  for (const el of queryAllDeep("div, span, li")) {
    if (el.closest("[data-cyberstrike-ui]")) continue;
    if (el.getAttribute("role")) continue;
    if (el.hasAttribute("onclick")) continue;
    if (!isStructurallyVisible(el)) continue;
    const text = (el.innerText || "").trim();
    if (!text || text.length > 80) continue;
    if (el.querySelector(INTERACTIVE_SELECTORS)) continue;
    if (window.getComputedStyle(el).cursor !== "pointer") continue;
    const parent = el.parentElement;
    if (parent && window.getComputedStyle(parent).cursor === "pointer") continue;
    const rect = el.getBoundingClientRect();
    if (rect.width >= window.innerWidth * 0.9 && rect.height >= window.innerHeight * 0.5) continue;
    addElement(el, "button", true);
  }

  // ---- Info elements (CAPTCHA, hints, contextual labels) ----
  const INTERACTIVE_TAGS = new Set(["input", "button", "a", "select", "textarea"]);
  const INTERACTIVE_ROLES = new Set([
    "button", "link", "menuitem", "tab",
    "checkbox", "radio", "combobox", "option", "slider", "textbox"
  ]);
  const infoSeen = new Set();
  const interactiveLabels = new Set(elements.map((e) => e.label.toLowerCase()));

  for (const el of document.querySelectorAll("[aria-label]")) {
    if (el.closest("[data-cyberstrike-ui]")) continue;
    const tag = el.tagName.toLowerCase();
    const role = (el.getAttribute("role") || "").toLowerCase();
    if (INTERACTIVE_TAGS.has(tag) || INTERACTIVE_ROLES.has(role)) continue;
    if (!isStructurallyVisible(el)) continue;
    const ariaLabel = (el.getAttribute("aria-label") || "").trim();
    if (!ariaLabel) continue;
    if (interactiveLabels.has(ariaLabel.toLowerCase())) continue;
    const text = (el.innerText || el.textContent || "").trim();
    if (!text || text.length > 150) continue;
    const key = "info::" + ariaLabel;
    if (infoSeen.has(key)) continue;
    infoSeen.add(key);
    elements.push({
      tag: tag,
      role: "info",
      label: ariaLabel,
      value: text,
      enabled: false,
      href: "",
      type: "",
      placeholder: "",
      options: "",
      constraints: "",
      selectorRole: "",
      selectorCSS: "",
      inChrome: false
    });
  }

  // Replace ambiguous role selectors (duplicated) with CSS selectors
  for (const el of elements) {
    if (el.selectorRole && (seenRoleSelectors.get(el.selectorRole) || 0) > 1 && el.selectorCSS) {
      el.selectorRole = "";
    }
  }

  return elements;
})()
