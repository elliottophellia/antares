import { useEffect, useMemo, useRef, useState } from 'react'
import { Broom, Pause, Play, Terminal } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { EmptyState, Input, Tabs, TabsList, TabsTrigger } from '@/components/ui/primitives'
import { Skeleton } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { usePageActions } from '@/components/layout/PageChrome'

interface LogEntry {
  time: string
  level: string
  message: string
  attrs?: Record<string, unknown>
}

const LEVELS = ['ALL', 'DEBUG', 'INFO', 'WARN', 'ERROR']

const LEVEL_TONE: Record<string, string> = {
  DEBUG: 'text-muted-foreground',
  INFO: 'text-[var(--success)]',
  WARN: 'text-[var(--warning)]',
  ERROR: 'text-destructive',
}

export default function LogsPage() {
  const { t, locale } = useI18n()
  const timeAgo = useTimeAgo()
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [level, setLevel] = useState('ALL')
  const [filter, setFilter] = useState('')
  const [live, setLive] = useState(true)
  const [loading, setLoading] = useState(true)
  const bottomRef = useRef<HTMLDivElement>(null)

  usePageActions(
    <>
      <Button variant="outline" size="sm" onClick={() => setLive((v) => !v)} className="gap-1.5">
        {live ? <Pause className="size-4" /> : <Play className="size-4" />}
        {live ? t('common.pause') : t('common.resume')}
      </Button>
      <Button variant="outline" size="sm" onClick={() => setEntries([])} className="gap-1.5">
        <Broom className="size-4" />
        <span className="hidden sm:inline">{t('common.clear')}</span>
      </Button>
    </>,
    [live, t],
  )

  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setInterval> | undefined

    const load = () =>
      get<{ entries: LogEntry[] }>(`/logs?limit=500&level=${level}`)
        .then((r) => {
          if (alive) setEntries(r.entries ?? [])
        })
        .catch(() => {})
        .finally(() => {
          if (alive) setLoading(false)
        })

    load()
    if (live) timer = setInterval(load, 2000)

    return () => {
      alive = false
      if (timer) clearInterval(timer)
    }
  }, [level, live])

  useEffect(() => {
    if (live) bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [entries, live])

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return entries
    return entries.filter((e) => e.message.toLowerCase().includes(q))
  }, [entries, filter])

  return (
    <PageLayout
      header={
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Tabs value={level} onValueChange={setLevel}>
            <TabsList>
              {LEVELS.map((l) => (
                <TabsTrigger key={l} value={l}>
                  {l}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t('logs.filterPlaceholder')}
            className="sm:max-w-xs"
          />
        </div>
      }
    >
      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 12 }).map((_, i) => (
            <Skeleton key={i} className="h-4 w-full" />
          ))}
        </div>
      ) : visible.length === 0 ? (
        <EmptyState icon={<Terminal className="size-8" />} title={t('logs.none')} />
      ) : (
        <div className="overflow-hidden rounded-[var(--radius-lg)] border border-border bg-card">
          <div className="p-3 font-mono text-[11px] leading-relaxed">
            {visible.map((e, i) => (
              <div key={i} className="flex gap-2 py-0.5">
                <span className="shrink-0 text-muted-foreground">
                  {new Date(e.time).toLocaleTimeString(locale, { hour12: false })}
                </span>
                <span className={cn('w-12 shrink-0 font-medium', LEVEL_TONE[e.level] ?? '')}>
                  {e.level}
                </span>
                <span className="min-w-0 break-words">
                  {e.message}
                  {e.attrs
                    ? Object.entries(e.attrs).map(([k, v]) => (
                        <span key={k} className="ml-2 text-muted-foreground">
                          {k}={String(v)}
                        </span>
                      ))
                    : null}
                </span>
              </div>
            ))}
            <div ref={bottomRef} />
          </div>
        </div>
      )}
      <p className="text-[11px] text-muted-foreground">
        {t('logs.lines', { n: visible.length, time: timeAgo(new Date()) })}
      </p>
    </PageLayout>
  )
}
