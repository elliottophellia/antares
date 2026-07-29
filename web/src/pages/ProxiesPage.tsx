import { useState } from 'react'
import { CheckCircle, GlobeHemisphereWest, Pencil, Plus, Trash, XCircle } from '@phosphor-icons/react'
import { del, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import {
  Badge,
  Card,
  CardContent,
  EmptyState,
  Input,
  Label,
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
import { Button } from '@/components/ui/button'
import { usePageActions } from '@/components/layout/PageChrome'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'

interface ProxyEntry {
  id: string
  label: string
  scheme: string
  host: string
  port: number
  username: string
  password: string // masked from the API
  url: string // masked from the API
}

interface ProxyList {
  active: string
  entries: ProxyEntry[]
}

export default function ProxiesPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ProxyList>('/proxies')
  const [editing, setEditing] = useState<ProxyEntry | null>(null)
  const [adding, setAdding] = useState(false)
  const [toRemove, setToRemove] = useState<ProxyEntry | null>(null)
  const [removing, setRemoving] = useState(false)
  const [busyActive, setBusyActive] = useState(false)

  usePageActions(
    <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
      <Plus className="size-4" />
      {t('proxies.add')}
    </Button>,
    [t],
  )

  const setActive = async (id: string) => {
    setBusyActive(true)
    try {
      await post('/proxies/select', { id })
      reload()
    } finally {
      setBusyActive(false)
    }
  }

  const confirmRemove = async () => {
    if (!toRemove) return
    setRemoving(true)
    try {
      await del(`/proxies/${encodeURIComponent(toRemove.id)}`)
      reload()
    } finally {
      setRemoving(false)
      setToRemove(null)
    }
  }

  if (loading && !data) return <SkeletonList count={3} />

  const entries = data?.entries ?? []
  const active = data?.active ?? ''

  return (
    <PageLayout>
      <ProxyDialog
        open={adding}
        onOpenChange={setAdding}
        onSaved={reload}
        makeActiveByDefault={entries.length === 0}
      />
      <ProxyDialog
        open={!!editing}
        entry={editing ?? undefined}
        onOpenChange={(o) => !o && setEditing(null)}
        onSaved={reload}
      />
      <ConfirmDialog
        open={!!toRemove}
        onOpenChange={(o) => !o && setToRemove(null)}
        title={t('proxies.removeTitle')}
        description={t('proxies.removeDesc', { label: toRemove?.label ?? '' })}
        confirmLabel={t('common.remove')}
        loading={removing}
        onConfirm={() => void confirmRemove()}
      />

      <p className="text-xs leading-relaxed text-muted-foreground sm:text-sm">
        {t('proxies.usedWhenActive')}
      </p>

      {entries.length === 0 ? (
        <EmptyState
          icon={<GlobeHemisphereWest className="size-6" />}
          title={t('proxies.none')}
          description={t('proxies.noneDesc')}
          action={
            <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
              <Plus className="size-4" />
              {t('proxies.add')}
            </Button>
          }
        />
      ) : (
        <div className="space-y-3">
          {/* Direct (no proxy) row — the way to turn the active proxy off. */}
          <ActiveRow
            selected={active === ''}
            busy={busyActive}
            title={t('proxies.direct')}
            subtitle={t('proxies.direct.desc')}
            onSelect={() => void setActive('')}
          />

          {entries.map((e) => (
            <Card key={e.id} className={cn(active === e.id && 'border-primary/50')}>
              <CardContent className="flex flex-wrap items-center gap-3 p-4">
                <button
                  onClick={() => void setActive(e.id)}
                  disabled={busyActive}
                  className={cn(
                    'flex size-5 shrink-0 items-center justify-center rounded-full border transition-colors',
                    active === e.id
                      ? 'border-primary bg-primary text-primary-foreground'
                      : 'border-muted-foreground/40 hover:border-primary/60',
                  )}
                  aria-label={t('proxies.setActive')}
                >
                  {active === e.id ? <CheckCircle className="size-4" weight="fill" /> : null}
                </button>

                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{e.label}</span>
                    {active === e.id ? <Badge variant="success">{t('proxies.active')}</Badge> : null}
                  </div>
                  <p className="truncate font-mono text-[11px] text-muted-foreground">{e.url}</p>
                </div>

                <div className="flex shrink-0 gap-1">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => setEditing(e)}
                    aria-label={t('proxies.edit')}
                  >
                    <Pencil className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => setToRemove(e)}
                    aria-label={t('common.remove')}
                  >
                    <Trash className="size-4" />
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </PageLayout>
  )
}

function ActiveRow({
  selected,
  busy,
  title,
  subtitle,
  onSelect,
}: {
  selected: boolean
  busy: boolean
  title: string
  subtitle: string
  onSelect: () => void
}) {
  const { t } = useI18n()
  return (
    <Card className={cn(selected && 'border-primary/50')}>
      <CardContent className="flex items-center gap-3 p-4">
        <button
          onClick={onSelect}
          disabled={busy}
          className={cn(
            'flex size-5 shrink-0 items-center justify-center rounded-full border transition-colors',
            selected
              ? 'border-primary bg-primary text-primary-foreground'
              : 'border-muted-foreground/40 hover:border-primary/60',
          )}
          aria-label={t('proxies.setActive')}
        >
          {selected ? <CheckCircle className="size-4" weight="fill" /> : null}
        </button>
        <div className="min-w-0 flex-1">
          <span className="font-medium">{title}</span>
          <p className="text-[11px] text-muted-foreground">{subtitle}</p>
        </div>
      </CardContent>
    </Card>
  )
}

const SCHEMES = ['http', 'https', 'socks5']

function ProxyDialog({
  open,
  entry,
  makeActiveByDefault,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  entry?: ProxyEntry
  makeActiveByDefault?: boolean
  onOpenChange: (o: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const isEdit = !!entry
  const [label, setLabel] = useState(entry?.label ?? '')
  const [scheme, setScheme] = useState(entry?.scheme || 'http')
  const [host, setHost] = useState(entry?.host ?? '')
  const [port, setPort] = useState(entry?.port ? String(entry.port) : '')
  const [username, setUsername] = useState(entry?.username ?? '')
  const [password, setPassword] = useState('')
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; ip?: string; error?: string } | null>(null)
  const [error, setError] = useState<string>()

  // A full URL box also accepts the common host:port:user:pass form.
  const bodyFor = () => {
    let u = url.trim()
    if (u && !u.includes('://')) {
      const parts = u.split(':')
      if (parts.length === 4) {
        u = `${scheme}://${encodeURIComponent(parts[2])}:${encodeURIComponent(parts[3])}@${parts[0]}:${parts[1]}`
      }
    }
    return {
      id: entry?.id,
      label: label.trim(),
      scheme,
      host: host.trim(),
      port: port ? Number(port) : 0,
      username: username.trim(),
      // A blank password on edit means "keep the stored one".
      password,
      url: u,
    }
  }

  const valid = url.trim() !== '' || host.trim() !== ''

  const test = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const r = await post<{ ok: boolean; ip?: string; error?: string }>('/proxies/test', bodyFor())
      setTestResult(r)
    } catch (e) {
      setTestResult({ ok: false, error: (e as Error).message })
    } finally {
      setTesting(false)
    }
  }

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const body = bodyFor() as Record<string, unknown>
      if (!isEdit && makeActiveByDefault) body.active = true
      await post('/proxies', body)
      onSaved()
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isEdit ? t('proxies.edit') : t('proxies.add')}</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <div className="space-y-1.5">
            <Label>{t('proxies.label')}</Label>
            <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t('proxies.labelPh')} />
          </div>

          <div className="grid grid-cols-[8rem_1fr_6rem] gap-2">
            <div className="space-y-1.5">
              <Label>{t('proxies.scheme')}</Label>
              <select
                value={scheme}
                onChange={(e) => setScheme(e.target.value)}
                className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
              >
                {SCHEMES.map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label>{t('proxies.host')}</Label>
              <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="proxy.example.com" />
            </div>
            <div className="space-y-1.5">
              <Label>{t('proxies.port')}</Label>
              <Input
                type="number"
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="8080"
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1.5">
              <Label>{t('proxies.username')}</Label>
              <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
            </div>
            <div className="space-y-1.5">
              <Label>{t('proxies.password')}</Label>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={isEdit ? '••••••••' : ''}
                autoComplete="off"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <Label>{t('proxies.url')}</Label>
            <Input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder={t('proxies.urlPh')}
              className="font-mono text-xs"
            />
            <p className="text-[11px] text-muted-foreground">{t('proxies.urlHint')}</p>
          </div>

          {testResult ? (
            <div
              className={cn(
                'flex items-center gap-2 rounded-[var(--radius-sm)] border p-2.5 text-xs',
                testResult.ok
                  ? 'border-[var(--success)]/40 text-foreground'
                  : 'border-destructive/40 text-destructive',
              )}
            >
              {testResult.ok ? (
                <CheckCircle className="size-4 text-[var(--success)]" weight="fill" />
              ) : (
                <XCircle className="size-4" weight="fill" />
              )}
              <span>
                {testResult.ok
                  ? t('proxies.testOk', { ip: testResult.ip ?? '' })
                  : t('proxies.testFail', { error: testResult.error ?? '' })}
              </span>
            </div>
          ) : null}

          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <Button variant="outline" onClick={() => void test()} loading={testing} disabled={!valid}>
            {testing ? t('proxies.testing') : t('proxies.test')}
          </Button>
          <DialogClose asChild>
            <Button variant="ghost">{t('common.close')}</Button>
          </DialogClose>
          <Button onClick={() => void submit()} loading={busy} disabled={!valid}>
            {t('proxies.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
