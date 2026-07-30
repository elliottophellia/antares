import { useEffect, useState } from 'react'
import { Check, Copy, DownloadSimple, ShieldCheck, Trash, Warning } from '@phosphor-icons/react'
import { del, downloadFile, get } from '@/lib/api'
import { copyText } from '@/lib/clipboard'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardHeader,
  CardTitle,
  EmptyState,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { SearchSelect } from '@/components/ui/SearchSelect'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { Markdown } from '@/components/chat/Markdown'

interface Phase {
  name: string
  title: string
  summary: string
  status: 'not_started' | 'in_progress' | 'complete' | 'blocked'
  evidence: number
}
interface Area {
  name: string
  title: string
  covered: boolean
}
interface Chain {
  name: string
  impact: string
}
interface Finding {
  id: string
  title: string
  severity: string
  target?: string
  status?: string
  cwe?: string
}
interface Intel {
  id: string
  type: string
  value: string
  detail?: string
}
interface Engagement {
  phases?: Phase[]
  coverage?: Area[]
  coverage_percent?: number
  chains?: Chain[]
  findings?: Finding[] | null
  intel?: Intel[] | null
  next?: string
}
interface SessionRow {
  id: string
  title: string
  findings: number
  intel: number
}

const PHASE_MARK: Record<Phase['status'], string> = {
  complete: '●',
  in_progress: '◐',
  blocked: '▲',
  not_started: '○',
}
const PHASE_COLOR: Record<Phase['status'], string> = {
  complete: 'text-[var(--success)]',
  in_progress: 'text-primary',
  blocked: 'text-[var(--warning)]',
  not_started: 'text-muted-foreground/50',
}
const SEV_VARIANT: Record<string, 'destructive' | 'warning' | 'secondary' | 'outline'> = {
  critical: 'destructive',
  high: 'destructive',
  medium: 'warning',
  low: 'secondary',
  info: 'outline',
}
const SEV_ORDER: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 }

export default function EngagementPage() {
  const { t } = useI18n()
  const { data: sessData, loading, reload } = useApi<{ sessions: SessionRow[] }>('/engagement/sessions')
  const sessions = sessData?.sessions ?? []
  const [sessionID, setSessionID] = useState('')
  const active = sessionID || sessions[0]?.id || ''
  const [tab, setTab] = useState<'overview' | 'raw'>('overview')
  const [toDelete, setToDelete] = useState<SessionRow | null>(null)
  const [deleting, setDeleting] = useState(false)

  const removeEngagement = async () => {
    if (!toDelete) return
    setDeleting(true)
    try {
      await del(`/engagement/${encodeURIComponent(toDelete.id)}`)
      if (sessionID === toDelete.id) setSessionID('')
      reload()
    } finally {
      setDeleting(false)
      setToDelete(null)
    }
  }

  const [eng, setEng] = useState<Engagement | null>(null)
  useEffect(() => {
    if (!active) {
      setEng(null)
      return
    }
    let cancelled = false
    get<Engagement>(`/engagement?session=${encodeURIComponent(active)}`)
      .then((d) => !cancelled && setEng(d))
      .catch(() => !cancelled && setEng(null))
    return () => {
      cancelled = true
    }
  }, [active])

  if (loading) {
    return (
      <PageLayout>
        <SkeletonList count={3} />
      </PageLayout>
    )
  }
  if (sessions.length === 0) {
    return (
      <PageLayout>
        <EmptyState
          icon={<ShieldCheck className="size-8" />}
          title={t('engagement.empty')}
          description={t('engagement.emptyDesc')}
        />
      </PageLayout>
    )
  }

  const activeTitle = sessions.find((s) => s.id === active)?.title || 'Security Assessment'
  const download = () =>
    downloadFile(
      `/engagement/report?session=${encodeURIComponent(active)}&title=${encodeURIComponent(activeTitle)}`,
      `report-${active}.md`,
    ).catch(() => {})

  const activeRow = sessions.find((s) => s.id === active)
  const header = (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <label className="text-xs text-muted-foreground">{t('engagement.pickSession')}</label>
        <div className="min-w-0 flex-1 sm:min-w-[18rem] sm:flex-none">
          <SearchSelect
            value={active}
            onChange={setSessionID}
            options={sessions.map((s) => ({
              value: s.id,
              label: s.title || s.id,
              hint: `${s.findings}✚ / ${s.intel}◆`,
            }))}
            placeholder={t('engagement.pickSession')}
            searchPlaceholder={t('engagement.searchSession')}
          />
        </div>
        <Button variant="outline" size="sm" disabled={!active} onClick={download} className="gap-1.5">
          <DownloadSimple className="size-3.5" />
          <span className="hidden sm:inline">{t('engagement.downloadReport')}</span>
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!activeRow}
          onClick={() => activeRow && setToDelete(activeRow)}
          aria-label={t('engagement.deleteSession')}
          className="gap-1.5 text-muted-foreground hover:text-destructive"
        >
          <Trash className="size-3.5" />
          <span className="hidden sm:inline">{t('common.delete')}</span>
        </Button>
      </div>
      <Tabs value={tab} onValueChange={(v) => setTab(v as 'overview' | 'raw')}>
        <TabsList>
          <TabsTrigger value="overview">{t('engagement.tabOverview')}</TabsTrigger>
          <TabsTrigger value="raw">{t('engagement.tabRaw')}</TabsTrigger>
        </TabsList>
      </Tabs>
    </div>
  )

  return (
    <PageLayout header={header}>
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={t('engagement.deleteTitle')}
        description={t('engagement.deleteDesc', { title: toDelete?.title || toDelete?.id || '' })}
        confirmLabel={t('common.delete')}
        loading={deleting}
        onConfirm={() => void removeEngagement()}
      />
      {tab === 'raw' ? (
        <RawReport session={active} title={activeTitle} />
      ) : (
        <Overview eng={eng} />
      )}
    </PageLayout>
  )
}

function Overview({ eng }: { eng: Engagement | null }) {
  const { t } = useI18n()
  const findings = [...(eng?.findings ?? [])].sort(
    (a, b) => (SEV_ORDER[a.severity] ?? 9) - (SEV_ORDER[b.severity] ?? 9),
  )
  const intel = eng?.intel ?? []
  const chains = eng?.chains ?? []
  const pct = eng?.coverage_percent ?? 0

  return (
    <div className="space-y-4">
      {/* Methodology */}
      <Card className="p-4">
        <CardHeader className="p-0 pb-3">
          <CardTitle className="text-sm">{t('engagement.methodology')}</CardTitle>
        </CardHeader>
        <div className="space-y-2">
          {(eng?.phases ?? []).map((p) => (
            <div key={p.name} className="flex items-start gap-2 text-sm">
              <span className={cn('w-4 shrink-0 text-center', PHASE_COLOR[p.status])}>
                {PHASE_MARK[p.status]}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{p.title}</span>
                  {p.evidence > 0 ? (
                    <Badge variant="secondary">{t('engagement.recorded', { n: p.evidence })}</Badge>
                  ) : null}
                </div>
                {p.summary ? (
                  <p className="text-[11px] text-muted-foreground">{p.summary}</p>
                ) : null}
              </div>
            </div>
          ))}
        </div>
        {eng?.next ? (
          <p className="mt-3 rounded-[var(--radius-sm)] border border-border bg-muted/30 px-2.5 py-1.5 text-[11px] text-muted-foreground">
            → {eng.next}
          </p>
        ) : null}
      </Card>

      {/* Coverage */}
      <Card className="p-4">
        <CardHeader className="p-0 pb-2">
          <CardTitle className="flex items-center justify-between text-sm">
            {t('engagement.coverage')}
            <span className="text-xs font-normal tabular-nums text-muted-foreground">{pct}%</span>
          </CardTitle>
        </CardHeader>
        <div className="mb-3 h-2 overflow-hidden rounded-full bg-muted">
          <div
            className="h-full rounded-full bg-primary transition-all"
            style={{ width: `${pct}%` }}
          />
        </div>
        <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3 lg:grid-cols-4">
          {(eng?.coverage ?? []).map((a) => (
            <div key={a.name} className="flex items-center gap-1.5 text-[11px]">
              {a.covered ? (
                <Check className="size-3.5 shrink-0 text-[var(--success)]" weight="bold" />
              ) : (
                <span className="size-3.5 shrink-0 rounded-[3px] border border-muted-foreground/40" />
              )}
              <span className={a.covered ? '' : 'text-muted-foreground'}>{a.title}</span>
            </div>
          ))}
        </div>
      </Card>

      {/* Chains */}
      {chains.length > 0 ? (
        <Card className="border-destructive/40 p-4">
          <CardHeader className="p-0 pb-2">
            <CardTitle className="flex items-center gap-2 text-sm text-destructive">
              <Warning className="size-4" weight="fill" />
              {t('engagement.chains')}
            </CardTitle>
          </CardHeader>
          <div className="space-y-1.5">
            {chains.map((c) => (
              <div key={c.name} className="text-xs">
                <span className="font-medium">{c.name}</span>
                <span className="text-muted-foreground"> — {c.impact}</span>
              </div>
            ))}
          </div>
        </Card>
      ) : null}

      {/* Findings */}
      <Card className="p-4">
        <CardHeader className="p-0 pb-2">
          <CardTitle className="text-sm">
            {t('engagement.findings')} ({findings.length})
          </CardTitle>
        </CardHeader>
        {findings.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t('engagement.noFindings')}</p>
        ) : (
          <div className="space-y-1.5">
            {findings.map((f) => (
              <div
                key={f.id}
                className="flex flex-wrap items-center gap-2 rounded-[var(--radius-sm)] border border-border px-2.5 py-2 text-sm"
              >
                <Badge variant={SEV_VARIANT[f.severity] ?? 'outline'}>{f.severity}</Badge>
                <span className="min-w-0 flex-1 font-medium">{f.title}</span>
                {f.cwe ? (
                  <span className="shrink-0 text-[10px] text-muted-foreground">{f.cwe}</span>
                ) : null}
                {f.target ? (
                  <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                    {f.target}
                  </span>
                ) : null}
                {f.status && f.status !== 'new' && f.status !== 'confirmed' ? (
                  <Badge variant="outline">{f.status}</Badge>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* Intel */}
      {intel.length > 0 ? (
        <Card className="p-4">
          <CardHeader className="p-0 pb-2">
            <CardTitle className="text-sm">
              {t('engagement.intel')} ({intel.length})
            </CardTitle>
          </CardHeader>
          <div className="space-y-1">
            {intel.map((it) => (
              <div key={it.id} className="flex items-center gap-2 text-xs">
                <Badge variant="outline">{it.type}</Badge>
                <span className="min-w-0 truncate font-mono">{it.value}</span>
                {it.detail ? (
                  <span className="min-w-0 flex-1 truncate text-muted-foreground">— {it.detail}</span>
                ) : null}
              </div>
            ))}
          </div>
        </Card>
      ) : null}
    </div>
  )
}

// The full Markdown assessment report, rendered inline — the readable "raw"
// view of everything the report download would contain.
function RawReport({ session, title }: { session: string; title: string }) {
  const { t } = useI18n()
  const [md, setMd] = useState<string | null>(null)
  const [err, setErr] = useState<string>()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!session) return
    let cancelled = false
    setMd(null)
    setErr(undefined)
    get<string>(
      `/engagement/report?session=${encodeURIComponent(session)}&title=${encodeURIComponent(title)}`,
    )
      .then((text) => !cancelled && setMd(typeof text === 'string' ? text : String(text)))
      .catch((e) => !cancelled && setErr((e as Error).message))
    return () => {
      cancelled = true
    }
  }, [session, title])

  const copy = async () => {
    if (md && (await copyText(md))) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  if (err) return <p className="text-xs text-destructive">{err}</p>
  if (md === null) return <Skeleton className="h-64 w-full" />

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center justify-between">
        <CardTitle className="text-sm">{t('engagement.rawReport')}</CardTitle>
        <Button variant="outline" size="sm" onClick={copy} className="gap-1.5">
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? t('common.copied') : t('common.copy')}
        </Button>
      </div>
      <div className="prose-sm max-w-none text-[13px] leading-relaxed">
        <Markdown content={md} />
      </div>
    </Card>
  )
}
