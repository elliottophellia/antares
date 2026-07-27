import { useEffect, useState } from 'react'
import { Kanban } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { PageBody } from '@/components/layout/AppShell'
import { Card, EmptyState } from '@/components/ui/primitives'
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

export default function BoardPage() {
  const { t } = useI18n()
  const { data: sessData, loading } = useApi<{ sessions: SessionRow[] }>('/board/sessions')
  const sessions = sessData?.sessions ?? []
  const [sessionID, setSessionID] = useState('')
  const active = sessionID || sessions[0]?.id || ''

  const [columns, setColumns] = useState<Column[]>([])
  useEffect(() => {
    if (!active) {
      setColumns([])
      return
    }
    let cancelled = false
    get<{ columns: Column[] }>(`/board?session=${encodeURIComponent(active)}`)
      .then((d) => !cancelled && setColumns(d.columns ?? []))
      .catch(() => !cancelled && setColumns([]))
    return () => {
      cancelled = true
    }
  }, [active])

  if (loading) return <PageBody><SkeletonList count={2} /></PageBody>
  if (sessions.length === 0) {
    return (
      <PageBody>
        <EmptyState icon={<Kanban className="size-8" />} title={t('board.empty')} description={t('board.emptyDesc')} />
      </PageBody>
    )
  }

  return (
    <PageBody>
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <label className="text-xs text-muted-foreground">{t('engagement.pickSession')}</label>
          <select
            value={active}
            onChange={(e) => setSessionID(e.target.value)}
            className="h-8 rounded-[var(--radius-sm)] border border-border bg-card px-2 text-sm"
          >
            {sessions.map((s) => (
              <option key={s.id} value={s.id}>
                {s.title || s.id}
              </option>
            ))}
          </select>
        </div>

        <div className="grid gap-3 md:grid-cols-3">
          {columns.map((col) => (
            <div key={col.name} className="space-y-2">
              <h3 className="flex items-center justify-between text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {COLUMN_TITLES[col.name] ?? col.name}
                <span className="text-[10px] font-normal">{col.cards.length}</span>
              </h3>
              <div className="space-y-2">
                {col.cards.length === 0 ? (
                  <div className="rounded-[var(--radius-sm)] border border-dashed border-border/60 p-4 text-center text-[11px] text-muted-foreground">
                    —
                  </div>
                ) : (
                  col.cards.map((c) => (
                    <Card key={c.id} className="p-3">
                      <p className="text-sm font-medium">{c.title}</p>
                      {c.note ? <p className="mt-1 text-xs text-muted-foreground">{c.note}</p> : null}
                    </Card>
                  ))
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </PageBody>
  )
}
