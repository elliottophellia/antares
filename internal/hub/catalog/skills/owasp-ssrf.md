---
name: owasp-ssrf
description: Test an authorized application for server-side request forgery. Use during authorized web/API testing.
tags: [security, owasp, ssrf]
triggers: [ssrf, server side request, url fetch, webhook]
---

# Server-side request forgery

SSRF is when you make the server fetch a URL of your choosing. On cloud
infrastructure it is often critical, because the server can reach the metadata
service and internal network you cannot. **Authorized targets only, and do not
pivot into infrastructure you are not authorized to touch.**

## Finding the surface

Anywhere the server fetches a URL on your behalf: webhooks, "import from URL",
PDF and image generators, link previews, URL health checks, XML parsers.

## Testing

1. Point it at a host you control and watch for the request arriving — that
   confirms the server fetches your URL.
2. Try internal addresses: `127.0.0.1`, `localhost`, `169.254.169.254`
   (cloud metadata), and internal ranges. A different response or a timeout is
   a signal.
3. Try schemes beyond http: `file://`, `gopher://`, `dict://` where the fetcher
   is permissive.
4. Defeat weak filters: decimal or hex IPs, a redirect from your host to an
   internal one, `[::]`, a DNS name that resolves to an internal address.

Confirm with a request that provably reached somewhere it should not — the
metadata endpoint returning cloud data, or your own server logging the hit.

Do not extract real cloud credentials to prove it — demonstrate the reach and
stop.

## Recording

The exact request, what internal resource it reached, the impact — with cloud
metadata this is often full compromise — and the fix: an allow-list of
destinations, not a block-list.
