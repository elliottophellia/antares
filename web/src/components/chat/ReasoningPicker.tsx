import { useEffect, useRef, useState } from 'react'
import { Brain, CaretDown, Check } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

// Reasoning effort options. Empty value means "use the configured default"
// (agent.reasoning_effort, then model.reasoning_effort). The rest map to the
// provider's thinking budget: none disables thinking, low/medium/high raise it.
const OPTIONS: { value: string; label: string; hint: string }[] = [
  { value: '', label: 'Default', hint: 'Use the configured effort' },
  { value: 'none', label: 'Off', hint: 'No reasoning' },
  { value: 'low', label: 'Low', hint: 'Brief reasoning' },
  { value: 'medium', label: 'Medium', hint: 'Balanced reasoning' },
  { value: 'high', label: 'High', hint: 'Deep reasoning' },
]

/**
 * Pick the reasoning effort for the next turn straight from the composer. The
 * choice rides on the message body (reasoning_effort) and overrides the
 * configured default for that turn only; it is remembered in localStorage so it
 * survives a reload. Mirrors RolePicker's compact chip style.
 */
export function ReasoningPicker({
  value,
  onChange,
  compact = false,
}: {
  value: string
  onChange: (effort: string) => void
  compact?: boolean
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  const current = OPTIONS.find((o) => o.value === value) ?? OPTIONS[0]

  const pick = (v: string) => {
    onChange(v)
    setOpen(false)
  }

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        title="Reasoning effort"
        className={cn(
          'flex items-center gap-1.5 border border-border bg-card transition-colors hover:border-primary/40 focus-visible:border-ring',
          compact
            ? 'h-8 rounded-[var(--radius-md)] px-2.5 text-xs'
            : 'h-[3.25rem] rounded-[var(--radius-xl)] px-3 text-sm shadow-sm',
        )}
      >
        <Brain className={cn('shrink-0 text-muted-foreground', compact ? 'size-3.5' : 'size-4')} />
        <span className="hidden max-w-24 truncate sm:inline">{current.label}</span>
        <CaretDown className="size-3 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute bottom-full left-0 z-30 mb-2 w-52 overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-card p-1 shadow-lg">
          {OPTIONS.map((o) => (
            <button
              key={o.value || 'default'}
              onClick={() => pick(o.value)}
              className={cn(
                'flex w-full items-start gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors hover:bg-muted',
                value === o.value && 'bg-primary/5',
              )}
            >
              {value === o.value ? (
                <Check className="mt-0.5 size-3.5 shrink-0 text-primary" />
              ) : (
                <span className="w-3.5 shrink-0" />
              )}
              <span className="min-w-0">
                <span className="block text-xs font-medium">{o.label}</span>
                <span className="block truncate text-[11px] text-muted-foreground">{o.hint}</span>
              </span>
            </button>
          ))}
        </div>
      ) : null}
    </div>
  )
}
