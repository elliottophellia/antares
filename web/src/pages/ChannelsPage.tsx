import { useEffect, useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  ChatCircle,
  DiscordLogo,
  Eye,
  EyeSlash,
  Key,
  Plugs,
  SlackLogo,
  TelegramLogo,
  Warning,
  WhatsappLogo,
} from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { PageBody } from '@/components/layout/AppShell'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  Input,
  Label,
  Switch,
} from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SkeletonList } from '@/components/ui/skeleton'

interface ChannelField {
  key: string
  label: string
  secret: boolean
  placeholder?: string
  set: boolean
}

interface Channel {
  id: string
  label: string
  enabled: boolean
  connected: boolean
  configured: boolean
  detail: string
  docs?: string
  fields: ChannelField[]
}

interface Pairing {
  id: string
  platform: string
  external_id: string
  display_name: string
  status: string
  created_at: string
}

interface ChannelsResponse {
  channels: Channel[]
  pairings: Pairing[]
}

const ICONS: Record<string, React.ComponentType<{ className?: string; weight?: 'fill' }>> = {
  telegram: TelegramLogo,
  discord: DiscordLogo,
  slack: SlackLogo,
  whatsapp: WhatsappLogo,
  matrix: ChatCircle,
  signal: ChatCircle,
  feishu: ChatCircle,
}

export default function ChannelsPage() {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const { data, loading, reload } = useApi<ChannelsResponse>('/channels')
  const [busy, setBusy] = useState('')
  const [configFor, setConfigFor] = useState<Channel | null>(null)

  const act = async (id: string, fn: () => Promise<unknown>) => {
    setBusy(id)
    try {
      await fn()
      reload()
    } finally {
      setBusy('')
    }
  }

  return (
    <PageBody>
      <ConfigDialog
        channel={configFor}
        onOpenChange={(open) => !open && setConfigFor(null)}
        onSaved={reload}
      />

      {loading && !data ? (
        <SkeletonList count={2} />
      ) : (
        <div className="space-y-3">
          {(data?.channels ?? []).map((c) => {
            const Icon = ICONS[c.id] ?? Plugs
            return (
              <Card key={c.id}>
                <CardHeader>
                  <div className="flex items-start gap-3">
                    <Icon className="mt-0.5 size-5 shrink-0 text-primary" weight="fill" />

                    <div className="min-w-0 flex-1">
                      <CardTitle className="flex flex-wrap items-center gap-2">
                        {c.label}
                        <Badge
                          variant={c.connected ? 'success' : c.enabled ? 'warning' : 'outline'}
                        >
                          {c.connected
                            ? t('channels.connected')
                            : c.enabled
                              ? t('channels.connecting')
                              : t('channels.disabled')}
                        </Badge>
                        {c.configured ? (
                          <Badge variant="secondary">
                            <CheckCircle className="size-3" weight="fill" />
                            {t('channels.tokenSet')}
                          </Badge>
                        ) : null}
                      </CardTitle>
                      <CardDescription>{c.detail}</CardDescription>

                      <div className="mt-3 flex flex-wrap items-center gap-2">
                        <Button
                          size="sm"
                          variant={c.configured ? 'outline' : 'default'}
                          onClick={() => setConfigFor(c)}
                          className="gap-1.5"
                        >
                          <Key className="size-4" />
                          {c.configured ? t('channels.changeToken') : t('channels.connect')}
                        </Button>
                        {!c.configured ? (
                          <span className="text-[11px] text-muted-foreground">
                            {t('channels.tokenNeeded')}
                          </span>
                        ) : null}
                      </div>
                    </div>

                    <Switch
                      checked={c.enabled}
                      disabled={busy === c.id || !c.configured}
                      onCheckedChange={(v) =>
                        act(c.id, () => post(`/channels/${c.id}/toggle`, { enabled: v }))
                      }
                      aria-label={`${t('common.enable')} ${c.label}`}
                      className="mt-1 shrink-0"
                    />
                  </div>
                </CardHeader>
              </Card>
            )
          })}
        </div>
      )}

      <div className="space-y-2">
        <h2 className="text-sm font-semibold">{t('channels.devices')}</h2>
        {loading && !data ? (
          <SkeletonList count={2} />
        ) : (data?.pairings.length ?? 0) === 0 ? (
          <EmptyState
            icon={<Plugs className="size-8" />}
            title={t('channels.noDevices')}
            description={t('channels.noDevicesDesc')}
          />
        ) : (
          <div className="space-y-2">
            {data!.pairings.map((p) => (
              <Card key={p.id} className="flex items-center gap-3 p-3.5">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline">{p.platform}</Badge>
                    <span className="truncate text-sm font-medium">
                      {p.display_name || p.external_id}
                    </span>
                    <Badge variant={p.status === 'approved' ? 'success' : 'warning'}>
                      {p.status}
                    </Badge>
                  </div>
                  <p className="mt-0.5 font-mono text-[10px] text-muted-foreground">
                    {p.external_id} · {timeAgo(p.created_at)}
                  </p>
                </div>
                {p.status === 'pending' ? (
                  <Button
                    size="sm"
                    loading={busy === p.id}
                    onClick={() => act(p.id, () => post('/pairing/approve', { id: p.id }))}
                  >
                    {t('common.approve')}
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    variant="outline"
                    loading={busy === p.id}
                    onClick={() => act(p.id, () => post('/pairing/revoke', { id: p.id }))}
                  >
                    {t('common.revoke')}
                  </Button>
                )}
              </Card>
            ))}
          </div>
        )}
      </div>
    </PageBody>
  )
}

/**
 * Credential entry, in a dialog rather than inline. Each channel declares the
 * fields it needs (from the server), so one form serves all of them — first
 * connection and rotating credentials later. Secret fields are masked; a field
 * left blank keeps its current value.
 */
function ConfigDialog({
  channel,
  onOpenChange,
  onSaved,
}: {
  channel: Channel | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [values, setValues] = useState<Record<string, string>>({})
  const [reveal, setReveal] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [saved, setSaved] = useState(false)
  const [needsRestart, setNeedsRestart] = useState(false)

  useEffect(() => {
    if (channel) {
      setValues({})
      setReveal({})
      setError(undefined)
      setSaved(false)
      setNeedsRestart(false)
    }
  }, [channel])

  // A newly-configured channel is enabled at once; an already-configured one
  // just has its credentials updated without flipping the switch.
  const missing = (channel?.fields ?? []).some(
    (f) => f.key !== 'listen_addr' && f.key !== 'path' && f.key !== 'user_id' && !f.set && !values[f.key]?.trim(),
  )

  const save = async () => {
    if (!channel) return
    setBusy(true)
    setError(undefined)
    try {
      const fields: Record<string, string> = {}
      for (const [k, v] of Object.entries(values)) if (v.trim()) fields[k] = v.trim()
      const r = await post<{ ok: boolean; error?: string; restart_required?: boolean }>(
        `/channels/${channel.id}/config`,
        { fields, enabled: channel.configured ? undefined : true },
      )
      if (!r.ok) {
        setError(r.error ?? t('channels.tokenRejected'))
        return
      }
      setSaved(true)
      setNeedsRestart(!!r.restart_required)
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!channel} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {channel?.configured
              ? t('channels.changeTokenTitle', { channel: channel?.label ?? '' })
              : t('channels.connectTitle', { channel: channel?.label ?? '' })}
          </DialogTitle>
          <DialogDescription>{channel?.detail}</DialogDescription>
        </DialogHeader>

        <DialogBody>
          {saved ? (
            <div className="space-y-3">
              <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-[var(--success)]/40 bg-[color-mix(in_oklch,var(--success)_10%,transparent)] p-3 text-xs">
                <CheckCircle className="mt-0.5 size-4 shrink-0 text-[var(--success)]" weight="fill" />
                <span className="min-w-0">{t('channels.tokenSet')} — {channel?.label}</span>
              </div>
              <p className="text-[11px] leading-relaxed text-muted-foreground">
                {needsRestart ? t('channels.restartHint') : t('channels.comingOnline')}
              </p>
            </div>
          ) : (
            <div className="space-y-3">
              {(channel?.fields ?? []).map((f) => {
                const isSecret = f.secret
                const shown = reveal[f.key]
                return (
                  <div key={f.key} className="space-y-1.5">
                    <Label htmlFor={`ch-${f.key}`} className="flex items-center gap-2">
                      {f.label}
                      {f.set ? (
                        <span className="text-[10px] font-normal text-muted-foreground">· {t('channels.tokenSet').toLowerCase()}</span>
                      ) : null}
                    </Label>
                    <div className="flex gap-2">
                      <Input
                        id={`ch-${f.key}`}
                        type={isSecret && !shown ? 'password' : 'text'}
                        value={values[f.key] ?? ''}
                        onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                        onKeyDown={(e) => e.key === 'Enter' && !missing && save()}
                        placeholder={f.placeholder ?? (f.set ? '••••••••' : '')}
                        autoComplete="off"
                      />
                      {isSecret ? (
                        <Button
                          variant="outline"
                          size="icon"
                          onClick={() => setReveal((r) => ({ ...r, [f.key]: !r[f.key] }))}
                          aria-label={t('config.reveal')}
                          className="shrink-0"
                        >
                          {shown ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
                        </Button>
                      ) : null}
                    </div>
                  </div>
                )
              })}

              {channel?.docs ? (
                <a
                  href={channel.docs}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex items-center gap-1.5 text-xs text-primary underline underline-offset-2"
                >
                  {t('channels.getToken', { channel: channel?.label ?? '' })}
                  <ArrowSquareOut className="size-3.5" />
                </a>
              ) : null}

              {error ? (
                <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
                  <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
                  <span className="min-w-0 break-words">{error}</span>
                </div>
              ) : null}
            </div>
          )}
        </DialogBody>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant={saved ? 'default' : 'outline'} size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          {!saved ? (
            <Button size="sm" onClick={save} loading={busy} disabled={missing}>
              {t('channels.verifyAndSave')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
