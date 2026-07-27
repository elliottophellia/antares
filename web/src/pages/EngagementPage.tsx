import { useEffect, useState } from 'react'
import { ShieldCheck, Warning } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { PageBody } from '@/components/layout/AppShell'
import { Badge, Card, CardHeader, CardTitle, EmptyState } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'

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
const SEV_VARIANT: Record<string, 'destructive' | 'warning' | 'secondary' | 'outline'> = {
  critical: 'destructive',
  high: 'destructive',
  medium: 'warning',
  low: 'secondary',
  info: 'outline',
}

export default function EngagementPage() {
  const { t } = useI18n()
  const { data: sessData, loading } = useApi<{ sessions: SessionRow[] }>('/engagement/sessions')
  const sessions = sessData?.sessions ?? []
  const [sessionID, setSessionID] = useState('')
  const active = sessionID || sessions[0]?.id || ''

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

  if (loading) return <PageBody><SkeletonList count={3} /></PageBody>
  if (sessions.length === 0) {
    return (
      <PageBody>
        <EmptyState icon={<ShieldCheck className="size-8" />} title={t('engagement.empty')} description={t('engagement.emptyDesc')} />
      </PageBody>
    )
  }

  const findings = eng?.findings ?? []
  const intel = eng?.intel ?? []
  const pct = eng?.coverage_percent ?? 0

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
                {s.title || s.id} ({s.findings}✚ / {s.intel}◆)
              </option>
            ))}
          </select>
        </div>

        {/* Methodology */}
        <Card className="p-4">
          <CardHeader className="p-0 pb-2">
            <CardTitle className="text-sm">{t('engagement.methodology')}</CardTitle>
          </CardHeader>
          <div className="space-y-1.5">
            {(eng?.phases ?? []).map((p) => (
              <div key={p.name} className="flex items-center gap-2 text-sm">
                <span className="w-4 text-center text-muted-foreground">{PHASE_MARK[p.status]}</span>
                <span className="font-medium">{p.title}</span>
                {p.evidence > 0 ? <span className="text-[11px] text-muted-foreground">{p.evidence} recorded</span> : null}
              </div>
            ))}
          </div>
          {eng?.next ? <p className="mt-2 text-[11px] text-muted-foreground">→ {eng.next}</p> : null}
        </Card>

        {/* Coverage */}
        <Card className="p-4">
          <CardHeader className="p-0 pb-2">
            <CardTitle className="flex items-center justify-between text-sm">
              {t('engagement.coverage')}
              <span className="text-xs font-normal text-muted-foreground">{pct}%</span>
            </CardTitle>
          </CardHeader>
          <div className="mb-3 h-2 overflow-hidden rounded-full bg-muted">
            <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${pct}%` }} />
          </div>
          <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-4">
            {(eng?.coverage ?? []).map((a) => (
              <div key={a.name} className="flex items-center gap-1.5 text-[11px]">
                <span className={a.covered ? 'text-[var(--success)]' : 'text-muted-foreground'}>
                  {a.covered ? '☑' : '☐'}
                </span>
                <span className={a.covered ? '' : 'text-muted-foreground'}>{a.title}</span>
              </div>
            ))}
          </div>
        </Card>

        {/* Chains */}
        {(eng?.chains ?? []).length > 0 ? (
          <Card className="border-destructive/40 p-4">
            <CardHeader className="p-0 pb-2">
              <CardTitle className="flex items-center gap-2 text-sm text-destructive">
                <Warning className="size-4" weight="fill" />
                {t('engagement.chains')}
              </CardTitle>
            </CardHeader>
            <div className="space-y-2">
              {eng!.chains!.map((c) => (
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
                <div key={f.id} className="flex flex-wrap items-center gap-2 text-sm">
                  <Badge variant={SEV_VARIANT[f.severity] ?? 'outline'}>{f.severity}</Badge>
                  <span className="font-medium">{f.title}</span>
                  {f.cwe ? <span className="text-[10px] text-muted-foreground">{f.cwe}</span> : null}
                  {f.target ? <span className="font-mono text-[10px] text-muted-foreground">{f.target}</span> : null}
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
                  <span className="font-mono">{it.value}</span>
                  {it.detail ? <span className="truncate text-muted-foreground">— {it.detail}</span> : null}
                </div>
              ))}
            </div>
          </Card>
        ) : null}
      </div>
    </PageBody>
  )
}
