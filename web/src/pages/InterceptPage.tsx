import { useEffect, useState } from 'react'
import { Check, Copy, DownloadSimple, Play, Plus, Stop, TrashSimple } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { copyText } from '@/lib/clipboard'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { Badge, Card, EmptyState, Input, Label } from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

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
  )

  return (
    <PageLayout header={header}>
      {selected ? <DetailDialog ex={selected} onClose={() => setSelected(null)} /> : null}

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
    </PageLayout>
  )
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
