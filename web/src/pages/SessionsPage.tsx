import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ChatCircleDots, Broom, MagnifyingGlass, Trash } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { formatCount } from '@/lib/utils'
import { PageBody } from '@/components/layout/AppShell'
import { Button } from '@/components/ui/button'
import { Badge, Card, EmptyState, Input } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { usePageActions } from '@/components/layout/PageChrome'

interface Session {
  id: string
  title: string
  platform: string
  model: string
  message_count: number
  tokens_in: number
  tokens_out: number
  updated_at: string
}

interface SessionList {
  sessions: Session[]
  total: number
}

interface SearchHit {
  session_id: string
  session_title: string
  role: string
  snippet: string
  created_at: string
}

export default function SessionsPage() {
  const navigate = useNavigate()
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [busy, setBusy] = useState(false)
  const [hits, setHits] = useState<SearchHit[] | null>(null)

  const { data, loading, reload } = useApi<SessionList>('/sessions?limit=100')
  const { data: emptyCount, reload: reloadEmpty } = useApi<{ count: number }>('/sessions/empty/count')

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query.trim()), 300)
    return () => clearTimeout(timer)
  }, [query])

  useEffect(() => {
    if (debounced.length < 2) {
      setHits(null)
      return
    }
    let alive = true
    get<{ hits: SearchHit[] }>(`/sessions/search?q=${encodeURIComponent(debounced)}`)
      .then((r) => alive && setHits(r.hits))
      .catch(() => alive && setHits([]))
    return () => {
      alive = false
    }
  }, [debounced])

  const sessions = useMemo(() => {
    const all = data?.sessions ?? []
    if (!debounced) return all
    const q = debounced.toLowerCase()
    return all.filter((s) => s.title.toLowerCase().includes(q))
  }, [data, debounced])

  const removeSession = async (id: string) => {
    setBusy(true)
    try {
      await del(`/sessions/${id}`)
      reload()
      reloadEmpty()
    } finally {
      setBusy(false)
    }
  }

  const purgeEmpty = async () => {
    setBusy(true)
    try {
      await post('/sessions/empty/delete')
      reload()
      reloadEmpty()
    } finally {
      setBusy(false)
    }
  }

  usePageActions(
    <>
      {emptyCount && emptyCount.count > 0 ? (
        <Button variant="outline" size="sm" onClick={purgeEmpty} loading={busy} className="gap-1.5">
          <Broom className="size-4" />
          <span className="hidden sm:inline">{t('sessions.cleanEmpty', { n: emptyCount.count })}</span>
          <span className="sm:hidden">{emptyCount.count}</span>
        </Button>
      ) : null}
      <Button size="sm" onClick={() => navigate('/')} className="gap-1.5">
        <ChatCircleDots className="size-4" />
        {t('common.new')}
      </Button>
    </>,
    [emptyCount, busy, t],
  )

  return (
    <PageBody>
      <div className="relative">
        <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('sessions.searchPlaceholder')}
          className="pl-9"
        />
      </div>

      {hits && hits.length > 0 ? (
        <section className="space-y-2">
          <p className="text-xs font-medium text-muted-foreground">
            {t('sessions.contentMatches', { n: hits.length })}
          </p>
          {hits.map((h, i) => (
            <Link key={`${h.session_id}-${i}`} to={`/c/${h.session_id}`}>
              <Card className="p-3.5 transition-colors hover:border-primary/40">
                <div className="flex items-center gap-2">
                  <Badge variant="outline">{h.role}</Badge>
                  <span className="truncate text-xs font-medium">{h.session_title || h.session_id}</span>
                  <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">
                    {timeAgo(h.created_at)}
                  </span>
                </div>
                <p className="mt-1.5 line-clamp-2 text-xs text-muted-foreground">{h.snippet}</p>
              </Card>
            </Link>
          ))}
        </section>
      ) : null}

      {loading && !data ? (
        <SkeletonList count={6} />
      ) : sessions.length === 0 ? (
        <EmptyState
          icon={<ChatCircleDots className="size-8" />}
          title={t('sessions.none')}
          description={t('sessions.noneDesc')}
          action={
            <Button size="sm" onClick={() => navigate('/')}>
              {t('sessions.startChat')}
            </Button>
          }
        />
      ) : (
        <div className="space-y-2">
          {sessions.map((s) => (
            <Card key={s.id} className="group flex items-center gap-3 p-3.5 transition-colors hover:border-primary/40">
              <Link to={`/c/${s.id}`} className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{s.title || t('sessions.untitled')}</p>
                <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
                  <Badge variant="outline">{s.platform}</Badge>
                  <span>{t('sessions.messages', { n: s.message_count })}</span>
                  <span>·</span>
                  <span>{t('sessions.tokens', { n: formatCount(s.tokens_in + s.tokens_out) })}</span>
                  <span>·</span>
                  <span>{timeAgo(s.updated_at)}</span>
                </div>
              </Link>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t('sessions.deleteOne')}
                disabled={busy}
                onClick={() => removeSession(s.id)}
                className="shrink-0 text-muted-foreground hover:text-destructive"
              >
                <Trash className="size-4" />
              </Button>
            </Card>
          ))}
        </div>
      )}
    </PageBody>
  )
}
