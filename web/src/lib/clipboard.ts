// Copy text to the clipboard, working around the fact that the async Clipboard
// API (navigator.clipboard) only exists in a secure context — HTTPS or
// localhost. Antares is often served over plain HTTP on a LAN address, where
// navigator.clipboard is undefined, so we fall back to a hidden <textarea> +
// document.execCommand('copy'), which works in insecure contexts too.
export async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Permission denied or blocked — fall through to the legacy path.
    }
  }
  return legacyCopy(text)
}

function legacyCopy(text: string): boolean {
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    // Keep it out of view and unfocusable-scrolling, but selectable.
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.top = '0'
    ta.style.left = '0'
    ta.style.width = '1px'
    ta.style.height = '1px'
    ta.style.padding = '0'
    ta.style.border = 'none'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    const selection = document.getSelection()
    const savedRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null
    ta.select()
    ta.setSelectionRange(0, text.length)
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    // Restore any prior selection so we don't disturb the page.
    if (savedRange && selection) {
      selection.removeAllRanges()
      selection.addRange(savedRange)
    }
    return ok
  } catch {
    return false
  }
}
