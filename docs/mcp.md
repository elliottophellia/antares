# MCP

The Model Context Protocol is a standard way for a program to expose tools to an
agent. Antares is a client: it connects to servers you configure and offers their
tools to the model alongside its own.

The quickest route is [the hub](hub.md) — seventeen servers with a one-click
install. This page is the underlying configuration.

## Configuration

```yaml
mcp:
  enabled: true
  servers:
    filesystem:
      enabled: true
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/you/data"]

    github:
      enabled: true
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      env:
        GITHUB_PERSONAL_ACCESS_TOKEN: ghp_…

    hosted:
      enabled: true
      transport: http
      url: https://mcp.example.com/sse
      headers:
        Authorization: Bearer …
```

| Field | Meaning |
|---|---|
| `transport` | `stdio` for a subprocess, `http` for streamable HTTP |
| `command` / `args` | How to start a stdio server |
| `env` | Environment for the subprocess — where credentials go |
| `url` / `headers` | Where to reach an HTTP server |
| `enabled` | Off keeps the entry without connecting |

## Naming

A server's tools arrive namespaced:

```
mcp__github__create_issue
mcp__filesystem__read_file
```

Two servers offering `read_file` therefore cannot collide, and the model can see
which server a tool came from.

## Failure is not fatal

A server that will not start is reported and skipped. Antares runs; the tools
from that server are missing and the MCP page says why.

That is deliberate: a broken `npx` install on a machine with no network should
not stop the agent from reading files.

## Checking

```
/mcp
```

Every configured server, whether it connected, how many tools it brought, and
the error if it did not. The dashboard's MCP page shows the same with each
server's tool list expandable.

## Prerequisites

Most published servers run through `npx` (Node) or `uvx`
([uv](https://docs.astral.sh/uv/)). Whichever the command names has to be on the
machine.

An HTTP server needs neither — it is a URL.

## Cost

Every tool from every connected server goes into the tool list on every turn.
Twenty servers with ten tools each is two hundred tool definitions in every
request, which is both expensive and worse at choosing.

Connect what you use. `enabled: false` keeps an entry ready without paying for
it.
