import { useEffect, useState } from 'react'
import { Broadcast, DownloadSimple, Play, Stop, Trash } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { PageBody } from '@/components/layout/AppShell'
import { Button } from '@/components/ui/button'
import { Badge, Card, EmptyState, Input, Label } from '@/components/ui/primitives'

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

export default function InterceptPage() {
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

  const statusColor = (s: number) =>
    s >= 500 ? 'destructive' : s >= 400 ? 'warning' : s >= 200 ? 'success' : 'outline'

  return (
    <PageBody>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Broadcast className="size-5 text-primary" />
        <div className="flex-1">
          <h1 className="text-lg font-semibold">Intercept proxy</h1>
          <p className="text-sm text-muted-foreground">
            MITM HTTP(S) proxy for authorized debugging of properties you control.
          </p>
        </div>
        {running ? (
          <>
            <Badge variant="success">Running · {status?.addr}</Badge>
            <Button variant="outline" size="sm" onClick={stop} className="gap-1.5">
              <Stop className="size-4" /> Stop
            </Button>
          </>
        ) : (
          <div className="flex items-center gap-2">
            <Input value={port} onChange={(e) => setPort(e.target.value)} className="w-24" placeholder="8899" />
            <Button size="sm" onClick={start} className="gap-1.5">
              <Play className="size-4" /> Start
            </Button>
          </div>
        )}
        <a href="/api/intercept/ca" download>
          <Button variant="outline" size="sm" className="gap-1.5">
            <DownloadSimple className="size-4" /> CA cert
          </Button>
        </a>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_360px]">
        {/* Capture log */}
        <Card className="overflow-hidden p-0">
          <div className="flex items-center justify-between border-b border-border px-3 py-2">
            <span className="text-sm font-medium">Traffic ({exchanges.length})</span>
            <Button variant="ghost" size="sm" onClick={clear} className="gap-1.5">
              <Trash className="size-3.5" /> Clear
            </Button>
          </div>
          {exchanges.length === 0 ? (
            <EmptyState title="No traffic yet" description="Point a client at the proxy and trust the CA cert." />
          ) : (
            <div className="max-h-[60vh] overflow-auto">
              {exchanges.map((e) => (
                <button
                  key={e.id}
                  onClick={() => setSelected(e)}
                  className="flex w-full items-center gap-2 border-b border-border px-3 py-1.5 text-left text-[12px] hover:bg-muted/40"
                >
                  <span className="w-14 shrink-0 font-mono font-medium">{e.method}</span>
                  <Badge variant={statusColor(e.status)} className="shrink-0">
                    {e.status}
                  </Badge>
                  <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">{e.url}</span>
                  {e.mocked ? <Badge variant="secondary">mock</Badge> : null}
                  {e.blocked ? <Badge variant="destructive">block</Badge> : null}
                  <span className="shrink-0 tabular-nums text-muted-foreground/60">{e.duration_ms}ms</span>
                </button>
              ))}
            </div>
          )}
        </Card>

        {/* Rules + detail */}
        <div className="space-y-4">
          <Card className="space-y-2 p-3">
            <Label>Rules</Label>
            <Input
              value={rule.match}
              onChange={(e) => setRule({ ...rule, match: e.target.value })}
              placeholder="URL substring to match"
            />
            <div className="flex gap-2">
              <Input
                value={rule.mock_status}
                onChange={(e) => setRule({ ...rule, mock_status: e.target.value })}
                placeholder="mock status"
                className="w-28"
              />
              <label className="flex items-center gap-1.5 text-sm text-muted-foreground">
                <input
                  type="checkbox"
                  checked={rule.block}
                  onChange={(e) => setRule({ ...rule, block: e.target.checked })}
                />
                block
              </label>
              <Button size="sm" onClick={addRule} className="ml-auto">
                Add
              </Button>
            </div>
            <Input
              value={rule.mock_body}
              onChange={(e) => setRule({ ...rule, mock_body: e.target.value })}
              placeholder="mock body (optional)"
            />
            <div className="space-y-1">
              {(rulesData?.rules ?? []).map((r) => (
                <div key={r.id} className="flex items-center gap-2 text-[12px]">
                  <span className="min-w-0 flex-1 truncate font-mono">{r.match}</span>
                  {r.block ? <Badge variant="destructive">block</Badge> : <Badge variant="secondary">mock {r.mock_status}</Badge>}
                  <Button variant="ghost" size="icon-sm" onClick={() => removeRule(r.id)}>
                    <Trash className="size-3.5" />
                  </Button>
                </div>
              ))}
            </div>
          </Card>

          {selected ? (
            <Card className="space-y-2 p-3 text-[12px]">
              <div className="font-mono font-medium">
                {selected.method} {selected.url} → {selected.status}
              </div>
              <details open>
                <summary className="cursor-pointer text-muted-foreground">Request</summary>
                <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap rounded bg-muted/50 p-2 font-mono">
                  {Object.entries(selected.req_headers || {})
                    .map(([k, v]) => `${k}: ${v.join(', ')}`)
                    .join('\n')}
                  {selected.req_body ? `\n\n${selected.req_body}` : ''}
                </pre>
              </details>
              <details open>
                <summary className="cursor-pointer text-muted-foreground">Response</summary>
                <pre className="mt-1 max-h-60 overflow-auto whitespace-pre-wrap rounded bg-muted/50 p-2 font-mono">
                  {Object.entries(selected.resp_headers || {})
                    .map(([k, v]) => `${k}: ${v.join(', ')}`)
                    .join('\n')}
                  {selected.resp_body ? `\n\n${selected.resp_body}` : ''}
                </pre>
              </details>
            </Card>
          ) : null}
        </div>
      </div>
    </PageBody>
  )
}
