import { useState } from 'react'
import { Brain, Database, MagnifyingGlass, Plus, Trash } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { PageBody } from '@/components/layout/AppShell'
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
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo } from '@/lib/i18n'

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

export default function MemoryPage() {
  const { t } = useI18n()
  return (
    <PageBody>
      <Tabs defaultValue="memory">
        <TabsList>
          <TabsTrigger value="memory" className="gap-1.5">
            <Brain className="size-3.5" /> {t('memory.tabMemory')}
          </TabsTrigger>
          <TabsTrigger value="rag" className="gap-1.5">
            <Database className="size-3.5" /> {t('memory.tabRag')}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="memory">
          <MemoryTab />
        </TabsContent>
        <TabsContent value="rag">
          <RagTab />
        </TabsContent>
      </Tabs>
    </PageBody>
  )
}

function MemoryTab() {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<MemoryItem[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [draft, setDraft] = useState({ key: '', content: '' })
  const { data, loading, reload } = useApi<{ memories: MemoryItem[] }>('/memory')

  const search = async () => {
    const q = query.trim()
    if (!q) {
      setResults(null)
      return
    }
    setBusy(true)
    try {
      const r = await get<{ memories: MemoryItem[] }>(`/memory/search?q=${encodeURIComponent(q)}`)
      setResults(r.memories)
    } finally {
      setBusy(false)
    }
  }

  const add = async () => {
    if (!draft.content.trim()) return
    setBusy(true)
    try {
      await post('/memory', { key: draft.key, content: draft.content, scope: 'global' })
      setDraft({ key: '', content: '' })
      reload()
    } finally {
      setBusy(false)
    }
  }

  const remove = async (id: string) => {
    setBusy(true)
    try {
      await del(`/memory/${id}`)
      reload()
      setResults((r) => r?.filter((m) => m.id !== id) ?? null)
    } finally {
      setBusy(false)
    }
  }

  const items = results ?? data?.memories ?? []

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>{t('memory.add')}</CardTitle>
          <CardDescription>{t('memory.addDesc')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="mem-key">{t('memory.key')} ({t('common.optional')})</Label>
            <Input
              id="mem-key"
              value={draft.key}
              onChange={(e) => setDraft((d) => ({ ...d, key: e.target.value }))}
              placeholder={t('memory.keyPlaceholder')}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="mem-content">{t('memory.content')}</Label>
            <Textarea
              id="mem-content"
              value={draft.content}
              onChange={(e) => setDraft((d) => ({ ...d, content: e.target.value }))}
              placeholder={t('memory.contentPlaceholder')}
              className="h-20"
            />
          </div>
          <Button size="sm" onClick={add} loading={busy} disabled={!draft.content.trim()} className="gap-1.5">
            <Plus className="size-4" /> {t('common.save')}
          </Button>
        </CardContent>
      </Card>

      <div className="flex gap-2">
        <div className="relative flex-1">
          <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && search()}
            placeholder={t('memory.searchPlaceholder')}
            className="pl-9"
          />
        </div>
        <Button variant="outline" onClick={search} loading={busy}>
          {t('common.search')}
        </Button>
      </div>

      {loading && !data ? (
        <SkeletonList count={4} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<Brain className="size-8" />}
          title={t('memory.none')}
          description={t('memory.noneDesc')}
        />
      ) : (
        <div className="space-y-2">
          {items.map((m) => (
            <Card key={m.id} className="flex items-start gap-3 p-3.5">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline">{m.scope}</Badge>
                  <span className="font-mono text-[11px] font-medium">{m.key}</span>
                  <span className="text-[10px] text-muted-foreground">{timeAgo(m.updated_at)}</span>
                </div>
                <p className="mt-1 text-xs">{m.content}</p>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => remove(m.id)}
                aria-label={t('common.delete')}
                className="text-muted-foreground hover:text-destructive"
              >
                <Trash className="size-4" />
              </Button>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}

function RagTab() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<RagStatus>('/rag/status')
  const [path, setPath] = useState('')
  const [collection, setCollection] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<string>()

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
              <Badge key={c} variant="secondary">
                {c}
              </Badge>
            ))}
          </CardContent>
        ) : null}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('memory.indexDocs')}</CardTitle>
          <CardDescription>{t('memory.indexDocsDesc')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
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
            <Label htmlFor="rag-col">{t('memory.collection')} ({t('common.optional')})</Label>
            <Input
              id="rag-col"
              value={collection}
              onChange={(e) => setCollection(e.target.value)}
              placeholder={t('memory.collectionPlaceholder')}
            />
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
