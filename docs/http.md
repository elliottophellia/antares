# HTTP requests

`web_fetch` reads a page as text. `http_request` is for talking to an API:
methods, headers, request bodies, and the full response — status, protocol, and
headers included.

What makes it different from a plain HTTP client is the wire. Bot-detection
services do not just read the `User-Agent`; they fingerprint the TLS handshake
(JA3/JA4), the HTTP/2 SETTINGS frame, and the exact order of headers. A stock Go
client has an unmistakable signature and gets a `403` before the request body is
ever read. `http_request` presents the handshake of a real Chrome, so the
request looks like it came from a browser at the cryptographic level.

## Using it

```
http_request method=GET url=https://api.example.com/v1/items
  headers={"Authorization": "Bearer …"}

http_request method=POST url=https://api.example.com/v1/items
  json={"name": "example", "tags": ["a", "b"]}
```

| Argument | What it does |
|---|---|
| `url` | Absolute http(s) URL (required) |
| `method` | `GET` (default), `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS` |
| `headers` | Request headers as a JSON object |
| `body` | Raw request body |
| `json` | JSON body; sets `Content-Type: application/json` and overrides `body` |
| `content_type` | Content-Type for a raw body (JSON is inferred when it looks like JSON) |
| `preset` | Fingerprint override for this one call |
| `max_chars` | Cap on the response body returned |

The response comes back as a status line with the negotiated protocol and the
fingerprint used, then the response headers, then the body.

## Fingerprints

The `preset` decides which browser is imitated. `chrome-131` is the default and
the recommended one; `chrome-131-windows`, `chrome-131-macos`, and `chrome-133`
are also available. Set a default in config, or override per call.

```yaml
tools:
  http:
    preset: chrome-131
    proxy: ""                # http://host:3128 or socks5://host:1080
    timeout_seconds: 30
    wrap_terminal: true      # route terminal curl/wget through this too
```

HTTP/1.1, HTTP/2, and HTTP/3 are negotiated automatically, along with cookies
and redirects.

## curl and wget in the terminal

The agent often reaches for `curl` or `wget` out of habit. With
`wrap_terminal` on (the default), those commands in the terminal are routed
through the same fingerprinted client — no change in how they are called.

It works by putting small `curl` and `wget` shims on the shell's PATH. Each
shim re-invokes Antares, which parses the common flags (`-X`, `-H`, `-d`,
`--json`, `-o`, `-I`, `-u`, `-A`, `-b`, bundled short flags, and more) and makes
the request with the browser fingerprint. Anything a shim does not fully
understand — a multipart upload (`-F`), a unix socket, an FTP URL, `--resolve` —
is handed to the **real** `curl`/`wget` untouched, so nothing that worked before
breaks.

This is a convenience layer over `http_request`, which remains the direct,
structured way to call an API. Turn it off with `wrap_terminal: false`; the
terminal then uses the system's own `curl`/`wget`. On Windows the shims are not
installed and the terminal always uses the system binaries.

## Approval

`http_request` is gated for approval: it reaches the network and can send
state-changing requests (`POST`, `PUT`, `DELETE`) to external services. Approve
each call, or set `tools.approval_mode: auto` if you trust the run.

## Use it for access, not abuse

The fingerprinting is for reaching APIs you are authorised to use that happen to
sit behind a bot-detection layer — your own services, authorised testing
targets, research within scope. It is not a licence to break a service's terms
or hammer an endpoint you do not own.
