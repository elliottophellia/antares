# The browser

`web_fetch` only sees what the server sends. For anything client-rendered that
is a blank shell, and for anything behind a login it is nothing at all. The
`browser` tool drives a real Chromium instead.

It speaks the Chrome DevTools Protocol directly. There is no driver binary, no
Node, and no Playwright install — just a browser that is already on the machine.

## How the agent uses it

The loop is: navigate, snapshot, act on a reference, snapshot again.

```
browser action=navigate url=https://example.com/login
→ Sign in — https://example.com/login
  e1 textbox "Email"
  e2 textbox "Password"
  e3 button "Sign in"
  e4 link "Forgot your password?" → /reset

browser action=type ref=e1 text=me@example.com
browser action=type ref=e2 text=… submit=true
→ typed into e2 and submitted — Dashboard — https://example.com/app
  e1 link "Projects" → /projects
  …
```

Pages are described, not screenshotted. A screenshot needs a vision model and
pixel coordinates; a snapshot gives a text model something it can name.

### References

`e7` refers to the element that was described as `e7` — not to a fresh query
that might match a different button after the page moved. The references are
held on the page, so a click resolves against the very element the model chose.

They stay valid until the page changes. After anything that navigates, submits,
or re-renders, take a fresh snapshot. A stale reference refuses with a message
saying so, rather than silently clicking the wrong thing.

Navigation and clicks return the new snapshot with them, so most steps need one
call rather than two.

## Actions

| Action | Arguments | What it does |
|---|---|---|
| `navigate` | `url` | Open a page and wait for it to settle |
| `snapshot` | `max` | List what can be acted on |
| `click` | `ref` | Activate an element |
| `type` | `ref`, `text`, `submit` | Clear a field and enter text |
| `select` | `ref`, `text` | Choose an option by value or label |
| `text` | `ref`, `max` | Read the page, or one region of it |
| `screenshot` | `full` | Write a PNG and return its path |
| `press` | `key` | Send a key to whatever has focus |
| `scroll` | `to`, `amount` | Move the page |
| `back` | | Go back in history |
| `wait_for` | `text`, `seconds` | Block until text appears |
| `eval` | `script` | Run JavaScript and return its value |
| `console` | | Drain console output since the last check |
| `requests` | | Drain the network log since the last check |
| `tabs` | | Where the page currently is |
| `close` | | Shut the browser down |

## The page persists

One browser per conversation, kept open between tool calls. That is what makes
multi-step work possible: log in once, then keep clicking.

An idle browser is reaped after fifteen minutes, because an abandoned one holds
several hundred megabytes that nobody would otherwise close. `action=close` ends
it immediately.

## Configuration

```yaml
tools:
  browser:
    enabled: true
    executable: ""                  # empty means look for one
    remote_url: ""                  # attach to a running Chrome instead
    user_data_dir: ~/.antares/browser
    headed: false                   # needs a display to be of any use
    width: 1280
    height: 800
```

**`executable`** — leave empty and Antares looks for Chrome, Chromium, Brave, or
Edge in the usual places for the platform, including the Chrome-for-Testing and
Playwright download locations.

**`remote_url`** — attach to a browser you started yourself:

```bash
google-chrome --remote-debugging-port=9222
```

```yaml
tools:
  browser:
    remote_url: http://127.0.0.1:9222
```

Useful when you want the agent working inside a session you have already logged
into by hand.

**`user_data_dir`** — the profile. Keeping it means cookies and logins survive a
restart. Point it at a throwaway directory if you would rather they did not.

**`headed`** — shows the window. On a headless server this fails; there is
nothing to show it on.

## Toolsets

`browser` is in the `default`, `research`, and `browser` toolsets. The last one
is browser plus web plus files, for a run that is only meant to work on the web:

```bash
antares config set tools.toolset browser
```

## What it will not do

There is no captcha solving and no bot-detection evasion. When a site blocks the
agent it stops and says so.

Downloads are not handled. A file the agent needs is better fetched with
`web_fetch` or `curl` through the terminal, where the path is explicit.

## When something goes wrong

**"no page is open yet"** — call `navigate` first. Every other action needs a
page.

**"e7 is no longer on the page"** — the page changed. Snapshot again.

**"Nothing interactive is on this page"** — usually a client-rendered app that
has not painted yet. `wait_for` some text you expect, then snapshot.

**A click that does nothing** — some widgets only respond to real key events.
Click, then `press`, or reach for `eval`.

**No browser found** — install Chrome or Chromium, or set
`tools.browser.executable` to the path of one.
