import { useEffect, useState } from 'react'
import { Kanban, TrashSimple, X } from '@phosphor-icons/react'
import { del, streamGet } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'

interface BoardCard {
  id: string
  title: string
  note?: string
  column: string
}
interface Column {
  name: string
  cards: BoardCard[]
}
interface SessionRow {
  id: string
  title: string
}

const COLUMN_TITLES: Record<string, string> = {
  todo: 'To do',
  doing: 'Doing',
  done: 'Done',
}
// Status dot colour per column, so the lanes read at a glance.
const COLUMN_DOT: Record<string, string> = {
  todo: 'bg-muted-foreground/40',
  doing: 'bg-primary',
  done: 'bg-[var(--success)]',
}

export default function BoardPage() {
  const { t } = useI18n()
  const { data: sessData, loading, reload: reloadSessions } = useApi<{ sessions: SessionRow[] }>(
    '/board/sessions',
  )
  const sessions = sessData?.sessions ?? []
  const [sessionID, setSessionID] = useState('')
  const active = sessionID || sessions[0]?.id || ''

  // Subscribe to the board's SSE stream: the server pushes the whole board once
  // immediately and again on every change, so the columns track the agent's
  // task list live, with no polling.
  const [columns, setColumns] = useState<Column[]>([])
  useEffect(() => {
    if (!active) {
      setColumns([])
      return
    }
    const close = streamGet(`/board/stream?session=${encodeURIComponent(active)}`, (e) => {
      const cols = (e as unknown as { columns?: Column[] }).columns
      if (cols) setColumns(cols)
    })
    return close
  }, [active])

  const removeCard = (id: string) =>
    del(`/board/card?session=${encodeURIComponent(active)}&id=${encodeURIComponent(id)}`).catch(
      () => {},
    )

  const clearBoard = () => {
    if (!active) return
    del(`/board?session=${encodeURIComponent(active)}`)
      .then(() => reloadSessions())
      .catch(() => {})
  }

  if (loading) {
    return (
      <PageLayout>
        <SkeletonList count={2} />
      </PageLayout>
    )
  }
  if (sessions.length === 0) {
    return (
      <PageLayout>
        <EmptyState
          icon={<Kanban className="size-8" />}
          title={t('board.empty')}
          description={t('board.emptyDesc')}
        />
      </PageLayout>
    )
  }

  const total = columns.reduce((n, c) => n + c.cards.length, 0)
  const done = columns.find((c) => c.name === 'done')?.cards.length ?? 0

  const header = (
    <div className="flex flex-wrap items-center gap-2">
      <label className="text-xs text-muted-foreground">{t('board.pickSession')}</label>
      <select
        value={active}
        onChange={(e) => setSessionID(e.target.value)}
        className="h-8 min-w-0 flex-1 rounded-[var(--radius-sm)] border border-border bg-card px-2 text-sm sm:flex-none"
      >
        {sessions.map((s) => (
          <option key={s.id} value={s.id}>
            {s.title || s.id}
          </option>
        ))}
      </select>
      {total > 0 ? (
        <span className="text-xs tabular-nums text-muted-foreground">
          {t('board.summary', { done, total })}
        </span>
      ) : null}
      <Button
        variant="outline"
        size="sm"
        onClick={clearBoard}
        disabled={!active || total === 0}
        className="ml-auto gap-1.5"
      >
        <TrashSimple className="size-3.5" />
        <span className="hidden sm:inline">{t('common.clear')}</span>
      </Button>
    </div>
  )

  return (
    <PageLayout header={header}>
      {total === 0 ? (
        <EmptyState
          icon={<Kanban className="size-8" />}
          title={t('board.noCards')}
          description={t('board.noCardsDesc')}
        />
      ) : (
        <div className="grid gap-3 md:grid-cols-3">
          {columns.map((col) => (
            <div
              key={col.name}
              className="flex min-h-0 flex-col rounded-[var(--radius-lg)] border border-border bg-muted/20"
            >
              <div className="flex items-center gap-2 border-b border-border px-3 py-2">
                <span className={cn('size-2 shrink-0 rounded-full', COLUMN_DOT[col.name] ?? 'bg-muted-foreground/40')} />
                <span className="text-xs font-semibold uppercase tracking-wide">
                  {COLUMN_TITLES[col.name] ?? col.name}
                </span>
                <span className="ml-auto rounded-full bg-background px-1.5 text-[10px] font-medium tabular-nums text-muted-foreground">
                  {col.cards.length}
                </span>
              </div>
              <div className="space-y-2 p-2.5">
                {col.cards.length === 0 ? (
                  <div className="rounded-[var(--radius-sm)] border border-dashed border-border/50 py-6 text-center text-[11px] text-muted-foreground/70">
                    {t('board.columnEmpty')}
                  </div>
                ) : (
                  col.cards.map((c) => (
                    <div
                      key={c.id}
                      className="group relative rounded-[var(--radius-sm)] border border-border bg-card p-2.5 shadow-sm"
                    >
                      <button
                        type="button"
                        onClick={() => removeCard(c.id)}
                        aria-label={t('common.delete')}
                        className="absolute right-1 top-1 rounded p-1 text-muted-foreground/40 opacity-0 transition hover:bg-muted hover:text-destructive group-hover:opacity-100"
                      >
                        <X className="size-3.5" />
                      </button>
                      <p
                        className={cn(
                          'pr-5 text-[13px] font-medium leading-snug',
                          col.name === 'done' && 'text-muted-foreground line-through',
                        )}
                      >
                        {c.title}
                      </p>
                      {c.note ? (
                        <p className="mt-1 text-[11px] text-muted-foreground">{c.note}</p>
                      ) : null}
                    </div>
                  ))
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </PageLayout>
  )
}
