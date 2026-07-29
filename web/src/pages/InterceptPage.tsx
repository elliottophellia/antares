import { useEffect, useState } from 'react'
import {
  Check,
  Copy,
  DownloadSimple,
  Pause,
  Play,
  Plus,
  Stop,
  TrashSimple,
} from '@phosphor-icons/react'
import { del, get, post, streamGet } from '@/lib/api'
import { copyText } from '@/lib/clipboard'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  EmptyState,
  Input,
  Label,
  Tabs,
  TabsList,
  TabsTrigger,
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

interface InterceptorRow {
  id: string
  label: string
  category: string
  available: boolean
  reason?: string
}
interface SessionRow {
  id: string
  interceptor: string
  info: Record<string, unknown>
}
interface Paused {
  id: number
  method: string
  url: string
  headers: Record<string, string>
  body: string
}

interface Exchange {
  id: number
  method: string
  url: string
  status: number
  duration_ms: number
  secure: boolean
  mocked: boolean
  blocked: boolean
  req_headers: Record<string, string[]>
  resp_headers: Record<string, string[]>
  req_body: string
  resp_body: string
}

interface Status {
  running: boolean
  addr: string
  exchanges: number
  rules: number
}

interface Rule {
  id: number
  match: string
  block: boolean
  mock_status: number
  mock_body: string
}

const statusColor = (s: number): 'destructive' | 'warning' | 'success' | 'outline' =>
  s >= 500 ? 'destructive' : s >= 400 ? 'warning' : s >= 200 ? 'success' : 'outline'

export default function InterceptPage() {
  const { t } = useI18n()
  const { data: status, reload: reloadStatus } = useApi<Status>('/intercept/status')
  const { data: rulesData, reload: reloadRules } = useApi<{ rules: Rule[] }>('/intercept/rules')
  const [exchanges, setExchanges] = useState<Exchange[]>([])
  const [selected, setSelected] = useState<Exchange | null>(null)
  const [port, setPort] = useState('8899')
  const [rule, setRule] = useState({ match: '', mock_status: '', mock_body: '', block: false })
  const [tab, setTab] = useState<'traffic' | 'interceptors' | 'breakpoints'>('traffic')

  const running = status?.running

  // Poll the capture log while running.
  useEffect(() => {
    let live = true
    const tick = () =>
      get<{ exchanges: Exchange[] }>('/intercept/exchanges')
        .then((d) => live && setExchanges(d.exchanges ?? []))
        .catch(() => {})
    tick()
    const id = setInterval(tick, 2000)
    return () => {
      live = false
      clearInterval(id)
    }
  }, [running])

  const start = async () => {
    await post('/intercept/start', { port: Number(port) || 8899 })
    reloadStatus()
  }
  const stop = async () => {
    await post('/intercept/stop', {})
    reloadStatus()
  }
  const clear = async () => {
    await post('/intercept/clear', {})
    setExchanges([])
    setSelected(null)
  }
  const addRule = async () => {
    if (!rule.match.trim()) return
    await post('/intercept/rules', {
      match: rule.match,
      block: rule.block,
      mock_status: Number(rule.mock_status) || 0,
      mock_body: rule.mock_body,
    })
    setRule({ match: '', mock_status: '', mock_body: '', block: false })
    reloadRules()
    reloadStatus()
  }
  const removeRule = async (id: number) => {
    await del(`/intercept/rules/${id}`)
    reloadRules()
  }

  const header = (
    <div className="space-y-3">
    <div className="flex flex-wrap items-center gap-2">
      {running ? (
        <>
          <Badge variant="success">
            {t('intercept.running')} · {status?.addr}
          </Badge>
          <Button variant="outline" size="sm" onClick={stop} className="gap-1.5">
            <Stop className="size-4" /> {t('intercept.stop')}
          </Button>
        </>
      ) : (
        <>
          <Badge variant="outline">{t('intercept.stopped')}</Badge>
          <Input
            value={port}
            onChange={(e) => setPort(e.target.value)}
            className="h-8 w-20"
            placeholder="8899"
          />
          <Button size="sm" onClick={start} className="gap-1.5">
            <Play className="size-4" /> {t('intercept.start')}
          </Button>
        </>
      )}
      <a href="/api/intercept/ca" download className="ml-auto">
        <Button variant="outline" size="sm" className="gap-1.5">
          <DownloadSimple className="size-4" /> {t('intercept.caCert')}
        </Button>
      </a>
      <Button
        variant="outline"
        size="sm"
        onClick={clear}
        disabled={exchanges.length === 0}
        className="gap-1.5"
      >
        <TrashSimple className="size-3.5" /> {t('common.clear')}
      </Button>
    </div>
    <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)}>
      <TabsList>
        <TabsTrigger value="traffic">{t('intercept.tabTraffic')}</TabsTrigger>
        <TabsTrigger value="interceptors">{t('intercept.tabInterceptors')}</TabsTrigger>
        <TabsTrigger value="breakpoints">{t('intercept.tabBreakpoints')}</TabsTrigger>
      </TabsList>
    </Tabs>
    </div>
  )

  return (
    <PageLayout header={header}>
      {selected ? <DetailDialog ex={selected} onClose={() => setSelected(null)} /> : null}

      {tab === 'interceptors' ? (
        <InterceptorsPanel running={!!running} />
      ) : tab === 'breakpoints' ? (
        <BreakpointsPanel running={!!running} />
      ) : (
      <div className="grid gap-4 lg:grid-cols-[1fr_340px]">
        {/* Capture log */}
        <Card className="overflow-hidden p-0">
          <div className="flex items-center justify-between border-b border-border px-3 py-2">
            <span className="text-sm font-medium">
              {t('intercept.traffic')} ({exchanges.length})
            </span>
          </div>
          {exchanges.length === 0 ? (
            <EmptyState title={t('intercept.noTraffic')} description={t('intercept.noTrafficDesc')} />
          ) : (
            <div className="max-h-[62vh] overflow-auto">
              {exchanges.map((e) => (
                <button
                  key={e.id}
                  onClick={() => setSelected(e)}
                  className="flex w-full items-center gap-2 border-b border-border px-3 py-1.5 text-left text-[12px] hover:bg-muted/40"
                >
                  <span className="w-14 shrink-0 font-mono font-medium">{e.method}</span>
                  <Badge variant={statusColor(e.status)} className="shrink-0">
                    {e.status || '—'}
                  </Badge>
                  <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">
                    {e.url}
                  </span>
                  {e.mocked ? <Badge variant="secondary">{t('intercept.mock')}</Badge> : null}
                  {e.blocked ? <Badge variant="destructive">{t('intercept.block')}</Badge> : null}
                  <span className="shrink-0 tabular-nums text-muted-foreground/60">
                    {e.duration_ms}ms
                  </span>
                </button>
              ))}
            </div>
          )}
        </Card>

        {/* Rules */}
        <Card className="space-y-2.5 p-3.5">
          <Label>{t('intercept.rules')}</Label>
          <Input
            value={rule.match}
            onChange={(e) => setRule({ ...rule, match: e.target.value })}
            placeholder={t('intercept.matchPlaceholder')}
          />
          <div className="flex gap-2">
            <Input
              value={rule.mock_status}
              onChange={(e) => setRule({ ...rule, mock_status: e.target.value })}
              placeholder={t('intercept.mockStatus')}
              className="w-28"
            />
            <label className="flex items-center gap-1.5 text-sm text-muted-foreground">
              <input
                type="checkbox"
                checked={rule.block}
                onChange={(e) => setRule({ ...rule, block: e.target.checked })}
              />
              {t('intercept.block')}
            </label>
            <Button size="sm" onClick={addRule} disabled={!rule.match.trim()} className="ml-auto gap-1.5">
              <Plus className="size-3.5" /> {t('intercept.addRule')}
            </Button>
          </div>
          <Input
            value={rule.mock_body}
            onChange={(e) => setRule({ ...rule, mock_body: e.target.value })}
            placeholder={t('intercept.mockBody')}
          />
          {(rulesData?.rules ?? []).length > 0 ? (
            <div className="space-y-1 border-t border-border pt-2">
              {(rulesData?.rules ?? []).map((r) => (
                <div key={r.id} className="flex items-center gap-2 text-[12px]">
                  <span className="min-w-0 flex-1 truncate font-mono">{r.match}</span>
                  {r.block ? (
                    <Badge variant="destructive">{t('intercept.block')}</Badge>
                  ) : (
                    <Badge variant="secondary">
                      {t('intercept.mock')} {r.mock_status || ''}
                    </Badge>
                  )}
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => removeRule(r.id)}
                    aria-label={t('common.delete')}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <TrashSimple className="size-3.5" />
                  </Button>
                </div>
              ))}
            </div>
          ) : (
            <p className="border-t border-border pt-2 text-[11px] text-muted-foreground">
              {t('intercept.noRules')}
            </p>
          )}
        </Card>
      </div>
      )}
    </PageLayout>
  )
}

// ---- interceptors panel -----------------------------------------------------

function InterceptorsPanel({ running }: { running: boolean }) {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{ interceptors: InterceptorRow[]; sessions: SessionRow[] }>(
    '/intercept/interceptors',
  )
  const [busy, setBusy] = useState('')
  const [env, setEnv] = useState<{ id: string; text: string } | null>(null)

  const activate = async (id: string) => {
    setBusy(id)
    try {
      const s = await post<SessionRow>('/intercept/activate', { interceptor: id })
      // Terminal returns env exports in info; surface them for copy.
      const envText = (s.info?.env as string) || ''
      if (envText) setEnv({ id: s.id, text: envText })
      reload()
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setBusy('')
    }
  }
  const deactivate = async (id: string) => {
    await del(`/intercept/sessions/${encodeURIComponent(id)}`).catch(() => {})
    reload()
  }

  if (loading && !data) return <SkeletonInline />

  const groups: Record<string, InterceptorRow[]> = {}
  for (const i of data?.interceptors ?? []) (groups[i.category] ??= []).push(i)

  return (
    <div className="space-y-4">
      {env ? (
        <EnvDialog text={env.text} onClose={() => setEnv(null)} />
      ) : null}

      {(data?.sessions ?? []).length > 0 ? (
        <Card className="p-3.5">
          <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {t('intercept.activeSessions')}
          </p>
          <div className="space-y-1.5">
            {data!.sessions.map((s) => (
              <div key={s.id} className="flex items-center gap-2 text-xs">
                <Badge variant="secondary">{s.interceptor}</Badge>
                <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">{s.id}</span>
                <Button variant="ghost" size="icon-sm" onClick={() => deactivate(s.id)} aria-label={t('common.delete')} className="text-muted-foreground hover:text-destructive">
                  <TrashSimple className="size-3.5" />
                </Button>
              </div>
            ))}
          </div>
        </Card>
      ) : null}

      {Object.entries(groups).map(([cat, items]) => (
        <div key={cat} className="space-y-2">
          <h2 className="text-sm font-semibold capitalize">{cat}</h2>
          <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
            {items.map((i) => (
              <div
                key={i.id}
                className={`rounded-[var(--radius-lg)] border border-border bg-card p-3.5 ${i.available ? '' : 'opacity-70'}`}
              >
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm font-medium">{i.label}</span>
                  <Badge variant={i.available ? 'success' : 'outline'}>
                    {i.available ? t('intercept.ready') : t('intercept.needsSetup')}
                  </Badge>
                </div>
                <code className="mt-0.5 inline-block rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                  {i.id}
                </code>
                {!i.available && i.reason ? (
                  <p className="mt-1.5 text-[11px] text-muted-foreground">{i.reason}</p>
                ) : null}
                <Button
                  size="sm"
                  variant={i.available ? 'default' : 'outline'}
                  disabled={!i.available || busy === i.id}
                  loading={busy === i.id}
                  onClick={() => activate(i.id)}
                  className="mt-2.5 w-full gap-1.5"
                >
                  <Play className="size-3.5" /> {t('intercept.activate')}
                </Button>
              </div>
            ))}
          </div>
        </div>
      ))}
      {!running ? (
        <p className="text-[11px] text-muted-foreground">{t('intercept.autoStartHint')}</p>
      ) : null}
    </div>
  )
}

function EnvDialog({ text, onClose }: { text: string; onClose: () => void }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    if (await copyText(text)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('intercept.terminalEnv')}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <p className="mb-2 text-xs text-muted-foreground">{t('intercept.terminalEnvDesc')}</p>
          <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] border border-border bg-muted/40 p-2.5 font-mono text-[11px] leading-relaxed">
            {text}
          </pre>
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">{t('common.close')}</Button>
          </DialogClose>
          <Button size="sm" onClick={copy} className="gap-1.5">
            {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            {copied ? t('common.copied') : t('common.copy')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- breakpoints panel ------------------------------------------------------

function BreakpointsPanel({ running }: { running: boolean }) {
  const { t } = useI18n()
  const [paused, setPaused] = useState<Paused[]>([])

  useEffect(() => {
    if (!running) {
      setPaused([])
      return
    }
    const close = streamGet('/intercept/breakpoints/stream', (e) => {
      const list = (e as unknown as { paused?: Paused[] }).paused
      if (list) setPaused(list)
    })
    return close
  }, [running])

  const resume = (id: number, abort: boolean) =>
    post(`/intercept/breakpoints/${id}${abort ? '?abort=1' : ''}`, {}).catch(() => {})

  if (!running) {
    return <EmptyState icon={<Pause className="size-8" />} title={t('intercept.bpProxyOff')} description={t('intercept.bpProxyOffDesc')} />
  }
  if (paused.length === 0) {
    return <EmptyState icon={<Pause className="size-8" />} title={t('intercept.bpNone')} description={t('intercept.bpNoneDesc')} />
  }
  return (
    <div className="space-y-2.5">
      {paused.map((p) => (
        <Card key={p.id} className="p-3.5">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="warning">
              <Pause className="size-3" weight="fill" /> {t('intercept.bpPaused')}
            </Badge>
            <span className="font-mono text-xs font-medium">{p.method}</span>
            <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{p.url}</span>
          </div>
          {p.body ? (
            <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] bg-muted/40 p-2 font-mono text-[10px]">
              {p.body}
            </pre>
          ) : null}
          <div className="mt-2.5 flex gap-2">
            <Button size="sm" onClick={() => resume(p.id, false)} className="gap-1.5">
              <Play className="size-3.5" /> {t('intercept.bpResume')}
            </Button>
            <Button size="sm" variant="outline" onClick={() => resume(p.id, true)} className="gap-1.5 text-destructive">
              <Stop className="size-3.5" /> {t('intercept.bpAbort')}
            </Button>
          </div>
        </Card>
      ))}
    </div>
  )
}

function SkeletonInline() {
  return <div className="h-40 animate-pulse rounded-[var(--radius-lg)] bg-muted/40" />
}

// Pretty-print a body if it is JSON; otherwise return it as-is.
function prettyBody(body: string): string {
  const trimmed = body.trim()
  if (!trimmed) return ''
  if (trimmed[0] === '{' || trimmed[0] === '[') {
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2)
    } catch {
      /* not JSON */
    }
  }
  return body
}

function headersText(h: Record<string, string[]>): string {
  return Object.entries(h || {})
    .map(([k, v]) => `${k}: ${v.join(', ')}`)
    .join('\n')
}

function DetailDialog({ ex, onClose }: { ex: Exchange; onClose: () => void }) {
  const { t } = useI18n()
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex min-w-0 items-center gap-2">
            <span className="font-mono">{ex.method}</span>
            <Badge variant={statusColor(ex.status)}>{ex.status || '—'}</Badge>
            <span className="text-xs font-normal text-muted-foreground">{ex.duration_ms}ms</span>
          </DialogTitle>
          <p className="min-w-0 break-all font-mono text-[11px] text-muted-foreground">{ex.url}</p>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <Section
            label={t('intercept.request')}
            headers={headersText(ex.req_headers)}
            body={ex.req_body}
          />
          <Section
            label={t('intercept.response')}
            headers={headersText(ex.resp_headers)}
            body={ex.resp_body}
          />
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Section({ label, headers, body }: { label: string; headers: string; body: string }) {
  const { t } = useI18n()
  const pretty = prettyBody(body)
  const full = [headers, pretty].filter(Boolean).join('\n\n')
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    if (await copyText(full)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          {label}
        </p>
        <button
          onClick={copy}
          className="inline-flex items-center gap-1 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
        >
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? t('common.copied') : t('common.copy')}
        </button>
      </div>
      {headers ? (
        <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] bg-muted/50 p-2 font-mono text-[10px] leading-relaxed">
          {headers}
        </pre>
      ) : null}
      {pretty ? (
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] border border-border bg-muted/30 p-2 font-mono text-[10px] leading-relaxed">
          {pretty}
        </pre>
      ) : (
        <p className="text-[11px] text-muted-foreground">{t('intercept.emptyBody')}</p>
      )}
    </div>
  )
}
