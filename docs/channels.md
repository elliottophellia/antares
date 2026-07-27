# Telegram and Discord

Both bots run inside the same process, share the same sessions, memory, and
tools, and answer with the same agent as the dashboard.

## Telegram

Long polling — no public domain, no webhook, no tunnel. It works on a laptop
behind NAT.

1. Talk to [@BotFather](https://t.me/BotFather), send `/newbot`, take the token.
2. Channels page → Telegram → **Connect** → paste it.

The token is verified with Telegram before it is saved, and the bot's name is
shown back so you can see you pasted the right one. Then flip the switch; the
gateway connects without a restart.

```yaml
gateway:
  enabled: true
  telegram:
    enabled: true
    bot_token: "…"
    require_pairing: true
    allowed_users: []
    allowed_chats: []
    stream_edits: true
```

`stream_edits` edits the reply as it is generated, so you watch it appear rather
than waiting for a wall of text.

## Discord

A websocket gateway connection.

1. [Developer Portal](https://discord.com/developers/applications) → New
   Application → Bot → copy the token.
2. Enable **Message Content Intent** under Privileged Gateway Intents. Without
   it the bot receives empty messages.
3. Channels page → Discord → **Connect** → paste it.
4. Invite it with `bot` and `applications.commands` scopes.

```yaml
gateway:
  discord:
    enabled: true
    bot_token: "…"
    require_pairing: true
    allowed_users: []
    allowed_guilds: []
```

## Access control

A bot token is a public address. Anyone who finds the bot can talk to it, and it
has your files and your shell. Two ways to close that.

**Pairing.** With `require_pairing: true`, a first message from an unknown
person creates a pending request instead of a conversation. Approve it on the
Channels page or with `/pairing approve`. Nothing runs until you do.

**Allow lists.** `allowed_users` and `allowed_chats` are exact ids. When either
is set, only listed ids get through and pairing is skipped.

Pairing suits a bot you might share. Allow lists suit one that is only ever
yours.

## In a thread

Everything in [Commands](commands.md) that makes sense without a screen works in
a message. `/status`, `/model`, `/skills`, `/goal`, `/memory`.

`/new` forgets the stored session so the next message starts fresh — the
messaging equivalent of clearing a transcript.

Group chats reply when addressed. Direct messages always reply.

## Toolsets per channel

A phone thread rarely wants a shell:

```yaml
tools:
  platform_toolsets:
    telegram: research
    discord: research
```

## Scheduled delivery

A cron job can deliver its output to a channel:

```yaml
target: telegram:123456789
```

The id is a chat id, which `/status` in that chat will tell you. See
[Scheduling](scheduling.md).

## When it will not connect

**Telegram says nothing.** Check the token with `/status`. A revoked token fails
verification at save time, so a token that saved is a token that worked.

**Discord connects but sees empty messages.** Message Content Intent is off.

**Neither reacts.** `gateway.enabled` must be true as well as the per-platform
switch. The Channels page shows the live connection state.
