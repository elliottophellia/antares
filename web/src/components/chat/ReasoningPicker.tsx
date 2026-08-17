import { useEffect, useRef, useState } from 'react'
import { Brain, CaretDown, Check } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { cn } from '@/lib/utils'

interface Capability {
  wire?: string
  values?: string[]
  default?: string
}

const LABELS: Record<string, { label: string; hint: string }> = {
  none: { label: 'Off', hint: 'Disable reasoning' },
  default: { label: 'Default', hint: 'Provider default on' },
  minimal: { label: 'Minimal', hint: 'Least thinking' },
  low: { label: 'Low', hint: 'Light reasoning' },
  medium: { label: 'Medium', hint: 'Balanced reasoning' },
  high: { label: 'High', hint: 'Deep reasoning' },
  xhigh: { label: 'Extra high', hint: 'Highest advertised effort' },
  max: { label: 'Max', hint: 'Maximum official effort' },
}

/**
 * Official-provider reasoning control. Options come from the server catalogue
 * for the active official model. Custom endpoints get no hardcoded ladder.
 */
export function ReasoningPicker({
  value,
  onChange,
  model,
  compact = false,
}: {
  value: string
  onChange: (effort: string) => void
  model?: string
  compact?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [cap, setCap] = useState<Capability>({})
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const q = model ? `?model=${encodeURIComponent(model)}` : ''
    get<{ reasoning_capability?: Capability }>(`/model/reasoning-capability${q}`)
      .then((d) => setCap(d.reasoning_capability ?? {}))
      .catch(() => setCap({}))
  }, [model])

  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  const values = cap.values ?? []
  useEffect(() => {
    if (value && values.length > 0 && !values.includes(value)) {
      onChange('')
    }
  }, [value, values, onChange])

  if (values.length === 0) return null

  const options = [
    { value: '', label: 'Auto', hint: cap.default ? `Provider default (${cap.default})` : 'Provider default' },
    ...values.map((v) => ({
      value: v,
      label: LABELS[v]?.label ?? v,
      hint: LABELS[v]?.hint ?? v,
    })),
  ]
  const current = options.find((o) => o.value === value) ?? options[0]

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        title="Reasoning"
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
        <div className="absolute bottom-full left-0 z-30 mb-2 w-56 overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-card p-1 shadow-lg">
          {options.map((o) => (
            <button
              key={o.value || 'auto'}
              onClick={() => {
                onChange(o.value)
                setOpen(false)
              }}
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
