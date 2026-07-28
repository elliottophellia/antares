import { useState } from 'react'
import { CaretDown, CaretUp, CheckCircle, Circle, CircleNotch } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useI18n } from '@/lib/i18n'

export type TodoItem = { content: string; status: string }

/** Parse the current task list out of a `todo` tool call's write args. */
export function parseTasks(rawArgs: string): TodoItem[] {
  try {
    const parsed = JSON.parse(rawArgs || '{}') as { items?: TodoItem[] }
    return Array.isArray(parsed.items) ? parsed.items : []
  } catch {
    return []
  }
}

// `live` is whether a turn is actively streaming. When it is not, an
// in_progress item shows a static dot instead of a spinner, so the checklist
// stops "running forever" once the run ends (whether it succeeded or failed).
const glyph = (status: string, live: boolean) =>
  status === 'completed' ? (
    <CheckCircle className="size-3.5 shrink-0 text-[var(--success)]" weight="fill" />
  ) : status === 'in_progress' ? (
    live ? (
      <CircleNotch className="size-3.5 shrink-0 animate-spin text-primary" />
    ) : (
      <Circle className="size-3.5 shrink-0 text-primary/60" weight="fill" />
    )
  ) : (
    <Circle className="size-3.5 shrink-0 text-muted-foreground/40" />
  )

/** A sticky task bar that butts up against the top of the composer, showing the
 *  agent's current checklist — collapsed to the active item + progress, or
 *  expanded to the full list. Mirrors a mission task bar. */
export function TaskBar({ tasks, live = false }: { tasks: TodoItem[]; live?: boolean }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  if (tasks.length === 0) return null

  const done = tasks.filter((it) => it.status === 'completed').length
  // Only surface a spinning "active" item while the turn is live.
  const active = live ? tasks.find((it) => it.status === 'in_progress') : undefined

  return (
    <div className="rounded-t-[var(--radius-md)] border-x border-t border-border bg-card/80 backdrop-blur">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
      >
        {active ? (
          <>
            <CircleNotch className="size-3.5 shrink-0 animate-spin text-primary" />
            <span className="min-w-0 flex-1 truncate text-[12px] text-foreground">
              {active.content}
            </span>
          </>
        ) : (
          <span className="min-w-0 flex-1 truncate text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            {t('chat.tasks')}
          </span>
        )}
        <span className="shrink-0 text-[11px] font-semibold tabular-nums text-muted-foreground">
          {done}/{tasks.length}
        </span>
        {open ? (
          <CaretDown className="size-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <CaretUp className="size-3.5 shrink-0 text-muted-foreground" />
        )}
      </button>

      {open ? (
        <ul className="max-h-48 space-y-1.5 overflow-y-auto border-t border-border px-3 py-2">
          {tasks.map((it, i) => (
            <li key={i} className="flex items-start gap-2 text-[12px] leading-snug">
              <span className="mt-0.5">{glyph(it.status, live)}</span>
              <span
                className={cn(
                  'min-w-0 flex-1',
                  it.status === 'completed'
                    ? 'text-muted-foreground line-through'
                    : it.status === 'in_progress'
                      ? 'font-medium text-foreground'
                      : 'text-muted-foreground',
                )}
              >
                {it.content}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}
