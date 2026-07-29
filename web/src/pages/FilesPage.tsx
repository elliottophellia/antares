import { useEffect, useState } from 'react'
import { ArrowUUpLeft, File as FileIcon, Folder, House } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { formatBytes } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Pagination } from '@/components/ui/Pagination'
import { Button } from '@/components/ui/button'
import { Card, EmptyState } from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { usePageActions } from '@/components/layout/PageChrome'

interface Entry {
  name: string
  path: string
  is_dir: boolean
  size: number
  modified: string
}

export default function FilesPage() {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [path, setPath] = useState('.')
  const [preview, setPreview] = useState<{ path: string; content: string } | null>(null)
  const [loadingPreview, setLoadingPreview] = useState(false)
  const [offset, setOffset] = useState(0)
  const PAGE = 20
  const { data, loading } = useApi<{ path: string; parent: string; entries: Entry[] }>(
    `/files?path=${encodeURIComponent(path)}`,
    [path],
  )

  useEffect(() => setOffset(0), [path])

  const open = async (entry: Entry) => {
    if (entry.is_dir) {
      setPath(entry.path)
      setPreview(null)
      return
    }
    setLoadingPreview(true)
    try {
      const r = await get<{ content: string }>(`/files/read?path=${encodeURIComponent(entry.path)}`)
      setPreview({ path: entry.path, content: r.content })
    } catch (e) {
      setPreview({ path: entry.path, content: t('files.cannotRead', { error: (e as Error).message }) })
    } finally {
      setLoadingPreview(false)
    }
  }

  usePageActions(
    <>
      <Button variant="outline" size="sm" onClick={() => setPath('.')} className="gap-1.5">
        <House className="size-4" />
      </Button>
      {data?.parent ? (
        <Button variant="outline" size="sm" onClick={() => setPath(data.parent)} className="gap-1.5">
          <ArrowUUpLeft className="size-4" />
          {t('files.up')}
        </Button>
      ) : null}
    </>,
    [data?.parent, t],
  )

  const entries = data?.entries ?? []
  const paged = entries.slice(offset, offset + PAGE)

  return (
    <PageLayout
      footer={<Pagination offset={offset} limit={PAGE} total={entries.length} onChange={setOffset} />}
    >
      <p className="truncate font-mono text-xs text-muted-foreground">{data?.path ?? path}</p>

      {loading && !data ? (
        <SkeletonList count={6} />
      ) : (data?.entries.length ?? 0) === 0 ? (
        <EmptyState icon={<Folder className="size-8" />} title={t('files.emptyFolder')} />
      ) : (
        <Card className="divide-y divide-border">
          {paged.map((e) => (
            <button
              key={e.path}
              onClick={() => open(e)}
              className="flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors hover:bg-accent"
            >
              {e.is_dir ? (
                <Folder className="size-4 shrink-0 text-primary" weight="fill" />
              ) : (
                <FileIcon className="size-4 shrink-0 text-muted-foreground" />
              )}
              <span className="min-w-0 flex-1 truncate text-sm">{e.name}</span>
              {!e.is_dir ? (
                <span className="shrink-0 text-[11px] text-muted-foreground">{formatBytes(e.size)}</span>
              ) : null}
              <span className="hidden shrink-0 text-[11px] text-muted-foreground sm:inline">
                {timeAgo(e.modified)}
              </span>
            </button>
          ))}
        </Card>
      )}

      {loadingPreview ? (
        <Skeleton className="h-64 w-full rounded-[var(--radius-lg)]" />
      ) : preview ? (
        <Card className="overflow-hidden">
          <div className="flex items-center justify-between border-b border-border px-3 py-2">
            <span className="truncate font-mono text-xs">{preview.path}</span>
            <Button variant="ghost" size="sm" onClick={() => setPreview(null)}>
              {t('common.close')}
            </Button>
          </div>
          <pre className="max-h-96 overflow-auto p-3 font-mono text-[11px] leading-relaxed">
            {preview.content}
          </pre>
        </Card>
      ) : null}
    </PageLayout>
  )
}
