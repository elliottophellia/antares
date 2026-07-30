import { useEffect, useRef, useState } from 'react'
import { CaretDown, Check, UsersThree, Warning } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'

interface Role {
  name: string
  title: string
  summary: string
  category: string
  danger?: boolean
  subrole?: boolean
}

const CATEGORY_LABEL: Record<string, string> = {
  general: 'General',
  engineering: 'Engineering',
  research: 'Research',
  writing: 'Writing',
  security: 'Security',
}

/**
 * Choose the specialist a conversation runs as, from the header — so the role
 * is a click, not a slash command. The selection rides on the next message and
 * is remembered by the server, so it sticks across turns.
 */
export function RolePicker({
  value,
  onChange,
  compact = false,
}: {
  value: string
  onChange: (role: string) => void
  // compact renders a short chip for the composer's control row, instead of the
  // tall standalone box.
  compact?: boolean
}) {
  const { t } = useI18n()
  const [roles, setRoles] = useState<Role[]>([])
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    get<{ roles: Role[] }>('/roles')
      // Subroles are reached only through their master role, never selected
      // directly — so they stay out of the picker.
      .then((r) => setRoles((r.roles ?? []).filter((x) => !x.subrole)))
      .catch(() => setRoles([]))
  }, [])

  // Close when clicking away.
  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  // Empty value means "the default agent", which is the Orchestrator (the
  // `assistant` role). Resolve it so the button and the checkmark reflect that,
  // rather than showing a separate "no role" state.
  const selected = value || 'assistant'
  const current = roles.find((r) => r.name === selected)

  // Group by category, preserving the server's order.
  const groups: { category: string; roles: Role[] }[] = []
  const index = new Map<string, number>()
  for (const r of roles) {
    if (!index.has(r.category)) {
      index.set(r.category, groups.length)
      groups.push({ category: r.category, roles: [] })
    }
    groups[index.get(r.category)!].roles.push(r)
  }

  const pick = (name: string) => {
    onChange(name)
    setOpen(false)
  }

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className={cn(
          'flex items-center gap-1.5 border border-border bg-card transition-colors hover:border-primary/40 focus-visible:border-ring',
          compact
            ? 'h-8 rounded-[var(--radius-md)] px-2.5 text-xs'
            : 'h-[3.25rem] rounded-[var(--radius-xl)] px-3 text-sm shadow-sm',
        )}
      >
        <UsersThree className={cn('shrink-0 text-muted-foreground', compact ? 'size-3.5' : 'size-4')} />
        <span className="hidden max-w-28 truncate sm:inline">
          {current ? current.title : t('roles.orchestrator')}
        </span>
        <CaretDown className="size-3 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute bottom-full left-0 z-30 mb-2 max-h-72 w-72 overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-card p-1 shadow-lg">
          {groups.map((g) => (
            <div key={g.category}>
              <div className="px-2.5 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                {CATEGORY_LABEL[g.category] ?? g.category}
              </div>
              {g.roles.map((r) => (
                <button
                  key={r.name}
                  // Selecting the Orchestrator (the default) clears the role back
                  // to empty, so the default stays represented as "no explicit
                  // role" rather than a pinned name.
                  onClick={() => pick(r.name === 'assistant' ? '' : r.name)}
                  className={cn(
                    'flex w-full items-start gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors hover:bg-muted',
                    selected === r.name && 'bg-primary/5',
                  )}
                >
                  {selected === r.name ? (
                    <Check className="mt-0.5 size-3.5 shrink-0 text-primary" />
                  ) : (
                    <span className="w-3.5 shrink-0" />
                  )}
                  <span className="min-w-0">
                    <span className="flex items-center gap-1.5 text-xs font-medium">
                      {r.title}
                      {r.danger ? <Warning className="size-3 text-[var(--warning)]" weight="fill" /> : null}
                    </span>
                    <span className="block truncate text-[11px] text-muted-foreground">{r.summary}</span>
                  </span>
                </button>
              ))}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}
