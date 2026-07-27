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
}: {
  value: string
  onChange: (role: string) => void
}) {
  const { t } = useI18n()
  const [roles, setRoles] = useState<Role[]>([])
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    get<{ roles: Role[] }>('/roles')
      .then((r) => setRoles(r.roles ?? []))
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

  const current = roles.find((r) => r.name === value)

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
        className="flex items-center gap-1.5 rounded-[var(--radius-sm)] border border-border px-2.5 py-1.5 text-xs transition-colors hover:border-primary/40"
      >
        <UsersThree className="size-3.5 text-muted-foreground" />
        <span className="max-w-32 truncate">{current ? current.title : t('roles.asAssistant')}</span>
        <CaretDown className="size-3 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute bottom-full left-0 z-30 mb-1 max-h-72 w-72 overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-card p-1 shadow-lg">
          <button
            onClick={() => pick('')}
            className={cn(
              'flex w-full items-center gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left text-xs transition-colors hover:bg-muted',
              value === '' && 'text-primary',
            )}
          >
            {value === '' ? <Check className="size-3.5" /> : <span className="w-3.5" />}
            {t('roles.asAssistant')}
          </button>

          {groups.map((g) => (
            <div key={g.category}>
              <div className="px-2.5 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                {CATEGORY_LABEL[g.category] ?? g.category}
              </div>
              {g.roles.map((r) => (
                <button
                  key={r.name}
                  onClick={() => pick(r.name)}
                  className={cn(
                    'flex w-full items-start gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors hover:bg-muted',
                    value === r.name && 'bg-primary/5',
                  )}
                >
                  {value === r.name ? (
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
