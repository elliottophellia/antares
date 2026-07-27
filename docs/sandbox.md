# Sandboxing

The agent has a shell. On a machine that also holds your files, your keys, and
your network, that is a lot of reach for something driven by a model following
instructions from a web page it just read.

```yaml
terminal:
  sandbox: auto        # none | auto | bubblewrap | namespace
  allow_network: false
```

Nothing here needs root.

## What each mode does

| Mode | Filesystem | Network | Needs |
|---|---|---|---|
| `none` | everything you can reach | open | — |
| `namespace` | unchanged | **gone** | Linux user namespaces |
| `bubblewrap` | **read-only except the workspace, credentials hidden** | gone unless allowed | `bwrap` installed |
| `auto` | the strongest of the above that works here | | — |

`auto` is the setting to use. It picks bubblewrap when installed, falls back to
namespaces, and says which it got — once, in the log, not on every command.

## Namespaces

Available on any Linux kernel with unprivileged user namespaces enabled, which
is most of them. The command runs in its own user, mount, PID, IPC, and UTS
namespaces, and — unless `allow_network` is true — its own empty network
namespace.

There is no filesystem confinement this way. Taking the network away is still
most of the value: an instruction the agent picked up from a page it read
cannot send anything anywhere.

## Bubblewrap

```bash
apt install bubblewrap     # or the equivalent
```

Then the filesystem is confined too:

- everything readable, nothing writable except the workspace
- a private `/tmp`
- credential directories replaced with empty ones

```yaml
terminal:
  sandbox: bubblewrap
  sandbox_hidden:
    - ~/.ssh
    - ~/.aws
    - ~/.gnupg
    - ~/.config/gh
    - ~/.antares/.env
    - ~/.kube
    - ~/.docker/config.json
```

That list is the default. Setting `sandbox_hidden` replaces it rather than
adding to it.

## Docker

For stronger separation, run commands in a container instead:

```yaml
terminal:
  backend: docker
  docker_image: debian:bookworm-slim
  allow_network: false
```

The workspace is mounted; nothing else is. This is the strongest option, and
the slowest to start.

## When it cannot be built

A missing mechanism downgrades and logs why, rather than refusing to run
anything — a sandbox you cannot have should not stop you working. Which means
you should check what you actually got:

```
antares doctor
```

A sandbox you believe in but do not have is worse than none, so the mode is
reported rather than assumed.

## What it does not do

It does not stop the agent from doing damage inside the workspace — that is
what [approvals](tools.md#approval) and [checkpoints](harness.md#undo) are for.

It does not filter the network, only remove it. There is no allow-list of hosts.

It does not confine the file tools, only the shell. Those are already bounded by
the workspace.
