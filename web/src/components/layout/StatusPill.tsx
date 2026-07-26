import { CheckCircle, WarningCircle, XCircle } from '@phosphor-icons/react'
import { usePoll } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

export interface StatusResponse {
  ok: boolean
  version: string
  model: string
  provider: string
  provider_ready: boolean
  database: string
  uptime_seconds: number
  active_sessions: number
}

/** Compact backend health indicator shown in the sidebar. */
export function StatusPill({ className }: { className?: string }) {
  const { t } = useI18n()
  const { data, loading, error } = usePoll<StatusResponse>('/status', 10000)

  if (loading && !data) {
    return <Skeleton className={cn('h-9 w-full rounded-[var(--radius-sm)]', className)} />
  }

  const offline = !!error || !data
  const degraded = !offline && !data!.provider_ready
  const Icon = offline ? XCircle : degraded ? WarningCircle : CheckCircle
  const tone = offline
    ? 'text-destructive'
    : degraded
      ? 'text-[var(--warning)]'
      : 'text-[var(--success)]'
  const label = offline ? t('status.offline') : degraded ? t('status.notReady') : t('status.connected')

  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-[var(--radius-sm)] border border-border px-2.5 py-1.5',
        className,
      )}
      title={offline ? t('status.offlineHint') : `${data?.provider} · ${data?.model}`}
    >
      <Icon className={cn('size-4 shrink-0', tone)} weight="fill" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-[11px] font-medium leading-tight">{label}</p>
        {data ? (
          <p className="truncate text-[10px] leading-tight text-muted-foreground">
            {data.model || t('status.noModel')}
          </p>
        ) : null}
      </div>
    </div>
  )
}
