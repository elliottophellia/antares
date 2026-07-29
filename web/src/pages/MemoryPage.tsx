import { useEffect, useState } from 'react'
import {
  Brain,
  Database,
  MagnifyingGlass,
  PencilSimple,
  Plus,
  TrashSimple,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { Pagination } from '@/components/ui/Pagination'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  Input,
  Label,
  Tabs,
  TabsList,
  TabsTrigger,
  Textarea,
} from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SkeletonList } from '@/components/ui/skeleton'

interface MemoryItem {
  id: string
  scope: string
  key: string
  content: string
  updated_at: string
}

interface RagStatus {
  enabled: boolean
  provider: string
  collections: string[]
  reachable: boolean
  detail: string
}

const PAGE = 24

export default function MemoryPage() {
  const { t } = useI18n()
  const [tab, setTab] = useState<'memory' | 'rag'>('memory')

  // Memory state lives here so the search box (in the sticky header) and the
  // grid (in the scrolling body) share it.
  const { data, loading, reload } = useApi<{ memories: MemoryItem[] }>('/memory')
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<MemoryItem[] | null>(null)
  const [searchBusy, setSearchBusy] = useState(false)
  const [offset, setOffset] = useState(0)
  const [editing, setEditing] = useState<MemoryItem | null>(null)
  const [creating, setCreating] = useState(false)

  const search = async () => {
    const q = query.trim()
    if (!q) {
      setResults(null)
      return
    }
    setSearchBusy(true)
    try {
      const r = await get<{ memories: MemoryItem[] }>(`/memory/search?q=${encodeURIComponent(q)}`)
      setResults(r.memories)
    } finally {
      setSearchBusy(false)
    }
  }

  const searching = results !== null
  const items = results ?? data?.memories ?? []
  const paged = searching ? items : items.slice(offset, offset + PAGE)
  useEffect(() => setOffset(0), [searching])

  const remove = async (id: string) => {
    await del(`/memory/${id}`)
    reload()
    setResults((r) => r?.filter((m) => m.id !== id) ?? null)
  }

  const header = (
    <div className="space-y-3">
      <Tabs value={tab} onValueChange={(v) => setTab(v as 'memory' | 'rag')}>
        <TabsList>
          <TabsTrigger value="memory" className="gap-1.5">
            <Brain className="size-3.5" /> {t('memory.tabMemory')}
          </TabsTrigger>
          <TabsTrigger value="rag" className="gap-1.5">
            <Database className="size-3.5" /> {t('memory.tabRag')}
          </TabsTrigger>
        </TabsList>
      </Tabs>

      {tab === 'memory' ? (
        <div className="flex gap-2">
          <div className="relative min-w-0 flex-1">
            <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && search()}
              placeholder={t('memory.searchPlaceholder')}
              className="pl-9"
            />
          </div>
          <Button variant="outline" onClick={search} loading={searchBusy} className="shrink-0">
            {t('common.search')}
          </Button>
          <Button onClick={() => setCreating(true)} className="shrink-0 gap-1.5">
            <Plus className="size-4" />
            <span className="hidden sm:inline">{t('common.new')}</span>
          </Button>
        </div>
      ) : null}
    </div>
  )

  const footer =
    tab === 'memory' && !searching ? (
      <Pagination offset={offset} limit={PAGE} total={items.length} onChange={setOffset} />
    ) : undefined

  return (
    <PageLayout header={header} footer={footer}>
      {(editing || creating) && (
        <MemoryEditor
          item={editing}
          onClose={() => {
            setEditing(null)
            setCreating(false)
          }}
          onSaved={() => {
            setEditing(null)
            setCreating(false)
            setResults(null)
            reload()
          }}
        />
      )}

      {tab === 'rag' ? (
        <RagTab />
      ) : loading && !data ? (
        <SkeletonList count={6} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<Brain className="size-8" />}
          title={searching ? t('memory.noMatch') : t('memory.none')}
          description={searching ? undefined : t('memory.noneDesc')}
          action={
            !searching ? (
              <Button size="sm" onClick={() => setCreating(true)}>
                {t('memory.add')}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
          {paged.map((m) => (
            <MemoryCard key={m.id} m={m} onEdit={() => setEditing(m)} onDelete={() => remove(m.id)} />
          ))}
        </div>
      )}
    </PageLayout>
  )
}

function MemoryCard({
  m,
  onEdit,
  onDelete,
}: {
  m: MemoryItem
  onEdit: () => void
  onDelete: () => void
}) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  return (
    <div className="group flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-3.5 transition-colors hover:border-primary/40">
      <button onClick={onEdit} className="min-w-0 flex-1 text-left">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="outline">{m.scope}</Badge>
          {m.key ? <span className="min-w-0 truncate font-mono text-[11px] font-medium">{m.key}</span> : null}
          <PencilSimple className="ml-auto size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
        </div>
        <p className="mt-1.5 line-clamp-4 text-xs">{m.content}</p>
      </button>
      <div className="mt-2.5 flex items-center justify-between border-t border-border pt-2">
        <span className="text-[10px] text-muted-foreground">{timeAgo(m.updated_at)}</span>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={onDelete}
          aria-label={t('common.delete')}
          className="text-muted-foreground hover:text-destructive"
        >
          <TrashSimple className="size-4" />
        </Button>
      </div>
    </div>
  )
}

function MemoryEditor({
  item,
  onClose,
  onSaved,
}: {
  item: MemoryItem | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const isNew = !item
  const [draft, setDraft] = useState({
    key: item?.key ?? '',
    content: item?.content ?? '',
    scope: item?.scope ?? 'global',
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()

  const save = async () => {
    if (!draft.content.trim()) return
    setSaving(true)
    setError(undefined)
    try {
      // Sending the existing id upserts (edit); omitting it creates a new one.
      await post('/memory', {
        id: item?.id ?? '',
        key: draft.key,
        content: draft.content,
        scope: draft.scope || 'global',
      })
      onSaved()
    } catch (e) {
      setError((e as Error).message)
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isNew ? t('memory.add') : t('memory.editMemory')}</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-3.5">
          <div className="space-y-1.5">
            <Label htmlFor="mem-content">{t('memory.content')}</Label>
            <Textarea
              id="mem-content"
              autoFocus
              value={draft.content}
              onChange={(e) => setDraft((d) => ({ ...d, content: e.target.value }))}
              placeholder={t('memory.contentPlaceholder')}
              className="h-32"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="mem-key">
                {t('memory.key')} ({t('common.optional')})
              </Label>
              <Input
                id="mem-key"
                value={draft.key}
                onChange={(e) => setDraft((d) => ({ ...d, key: e.target.value }))}
                placeholder={t('memory.keyPlaceholder')}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="mem-scope">{t('memory.scope')}</Label>
              <Input
                id="mem-scope"
                value={draft.scope}
                onChange={(e) => setDraft((d) => ({ ...d, scope: e.target.value }))}
                placeholder="global"
              />
            </div>
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button size="sm" onClick={save} loading={saving} disabled={!draft.content.trim()}>
            {t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- RAG ---------------------------------------------------------------------

interface RagResult {
  content: string
  path?: string
  doc_id?: string
  score: number
}

function RagTab() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<RagStatus>('/rag/status')
  const [path, setPath] = useState('')
  const [collection, setCollection] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string>()

  // Search-the-index state.
  const [query, setQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [results, setResults] = useState<RagResult[] | null>(null)
  const [searchErr, setSearchErr] = useState<string>()

  const index = async () => {
    if (!path.trim()) return
    setBusy(true)
    setMessage(undefined)
    try {
      const r = await post<{ chunks: number; files: number }>('/rag/index', {
        path: path.trim(),
        collection: collection.trim(),
      })
      setMessage(t('memory.indexed', { files: r.files, chunks: r.chunks }))
      reload()
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const runSearch = async () => {
    if (!query.trim()) return
    setSearching(true)
    setSearchErr(undefined)
    try {
      const r = await post<{ results: RagResult[] }>('/rag/search', {
        query: query.trim(),
        collection: collection.trim(),
      })
      setResults(r.results ?? [])
    } catch (e) {
      setSearchErr((e as Error).message)
      setResults(null)
    } finally {
      setSearching(false)
    }
  }

  const removeCollection = async (name: string) => {
    await del(`/rag/collections/${encodeURIComponent(name)}`).catch(() => {})
    reload()
  }

  if (loading && !data) return <SkeletonList count={3} />

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            {t('memory.ragStatus')}
            {data?.enabled ? (
              <Badge variant={data.reachable ? 'success' : 'destructive'}>{data.provider}</Badge>
            ) : (
              <Badge variant="outline">{t('memory.inactive')}</Badge>
            )}
          </CardTitle>
          <CardDescription>
            {data?.enabled
              ? data.detail || t('memory.ragBackend', { provider: data.provider })
              : t('memory.ragDisabled')}
          </CardDescription>
        </CardHeader>
        {data?.collections?.length ? (
          <CardContent className="flex flex-wrap gap-1.5">
            {data.collections.map((c) => (
              <span
                key={c}
                className="group inline-flex items-center gap-1 rounded-full border border-border bg-muted/40 py-0.5 pl-2.5 pr-1 text-xs"
              >
                {c}
                <button
                  onClick={() => removeCollection(c)}
                  aria-label={t('common.delete')}
                  className="rounded-full p-0.5 text-muted-foreground/50 transition-colors hover:bg-background hover:text-destructive"
                >
                  <TrashSimple className="size-3" />
                </button>
              </span>
            ))}
          </CardContent>
        ) : null}
      </Card>

      {/* Search the index. */}
      <Card>
        <CardHeader>
          <CardTitle>{t('memory.ragSearch')}</CardTitle>
          <CardDescription>{t('memory.ragSearchDesc')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex gap-2">
            <div className="relative min-w-0 flex-1">
              <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && runSearch()}
                placeholder={t('memory.ragSearchPlaceholder')}
                className="pl-9"
                disabled={!data?.enabled}
              />
            </div>
            <Button
              variant="outline"
              onClick={runSearch}
              loading={searching}
              disabled={!data?.enabled || !query.trim()}
              className="shrink-0"
            >
              {t('common.search')}
            </Button>
          </div>
          {searchErr ? <p className="text-xs text-destructive">{searchErr}</p> : null}
          {results !== null ? (
            results.length === 0 ? (
              <p className="text-xs text-muted-foreground">{t('memory.ragNoHits')}</p>
            ) : (
              <div className="space-y-2">
                {results.map((r, i) => (
                  <div key={i} className="rounded-[var(--radius-sm)] border border-border p-2.5">
                    <div className="mb-1 flex items-center gap-2">
                      {r.path ? (
                        <span className="min-w-0 truncate font-mono text-[11px] text-muted-foreground">
                          {r.path}
                        </span>
                      ) : null}
                      <Badge variant="secondary" className="ml-auto shrink-0 tabular-nums">
                        {r.score.toFixed(3)}
                      </Badge>
                    </div>
                    <p className="whitespace-pre-wrap break-words text-[11px] leading-relaxed text-foreground/90">
                      {r.content.length > 500 ? r.content.slice(0, 500) + '…' : r.content}
                    </p>
                  </div>
                ))}
              </div>
            )
          ) : null}
        </CardContent>
      </Card>

      {/* Index a path. */}
      <Card>
        <CardHeader>
          <CardTitle>{t('memory.indexDocs')}</CardTitle>
          <CardDescription>{t('memory.indexDocsDesc')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="rag-path">{t('memory.path')}</Label>
              <Input
                id="rag-path"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                placeholder={t('memory.pathPlaceholder')}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="rag-col">
                {t('memory.collection')} ({t('common.optional')})
              </Label>
              <Input
                id="rag-col"
                value={collection}
                onChange={(e) => setCollection(e.target.value)}
                placeholder={t('memory.collectionPlaceholder')}
              />
            </div>
          </div>
          <Button size="sm" onClick={index} loading={busy} disabled={!data?.enabled || !path.trim()}>
            {t('memory.indexNow')}
          </Button>
          {message ? <p className="text-xs text-muted-foreground">{message}</p> : null}
        </CardContent>
      </Card>
    </div>
  )
}
