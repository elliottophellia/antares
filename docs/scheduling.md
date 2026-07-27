# Scheduling

A scheduled job is a prompt that runs unattended. Not a script — the same agent,
with the same tools, given an instruction at a time you choose.

## Creating one

Dashboard → Automation → **New**. Name it, give it a schedule, write the prompt.
The next few run times are previewed as you type, so a wrong expression is
obvious before the job exists.

```bash
antares cron add "morning brief" "0 8 * * *" "Summarise anything that changed in ~/projects since yesterday"
antares cron list
antares cron run <id>        # run it now
antares cron rm <id>
```

## Expressions

Standard five fields — minute, hour, day of month, month, day of week:

```
0 8 * * *        08:00 daily
*/15 * * * *     every fifteen minutes
0 9 * * 1-5      09:00 on weekdays
0 0 1 * *        midnight on the first
```

Plus shorthands:

```
@hourly  @daily  @weekly  @monthly
@every 90m       @every 6h       @every 30s
```

```yaml
cron:
  enabled: true
  timezone: Local        # or Asia/Jakarta, UTC, …
  max_concurrent: 2
  history_limit: 50
```

## Writing the prompt

A job runs with no one watching, so the prompt has to be complete. Say where to
look, what to produce, and what to do when there is nothing to report.

Bad:

> check the logs

Good:

> Read /var/log/app/error.log for entries since yesterday 08:00. Group them by
> message. If there are none, reply exactly "nothing overnight". Otherwise list
> each group with its count and the newest timestamp.

Unattended runs are where [verification](harness.md) earns its cost — nobody is
there to notice work that was described but not done.

## Delivery

Without a target, output goes to a session you can read later. With one, it is
sent:

```yaml
target: telegram:123456789
target: discord:987654321
```

The format is `platform:channel`. A job with a target that is not connected
records the failure rather than losing the output.

## History

Every run records its outcome, duration, and output. The Automation page shows
the last state on each job, and the run history behind it — the fastest way to
tell "this has been failing for three days" from "this failed once".

## Turning one off

The switch on each job pauses it without deleting it. `cron.enabled: false`
stops the scheduler entirely.
