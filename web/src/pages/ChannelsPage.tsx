import { useEffect, useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  DiscordLogo,
  Eye,
  EyeSlash,
  Key,
  Plugs,
  TelegramLogo,
  Warning,
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

interface Channel {
  id: string
  label: string
  enabled: boolean
  connected: boolean
  detail: string
  has_token: boolean
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
}

/** Where each platform hands out bot tokens. */
const TOKEN_URLS: Record<string, string> = {
  telegram: 'https://t.me/BotFather',
  discord: 'https://discord.com/developers/applications',
}

export default function ChannelsPage() {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const { data, loading, reload } = useApi<ChannelsResponse>('/channels')
  const [busy, setBusy] = useState('')
  const [tokenFor, setTokenFor] = useState<Channel | null>(null)

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
      <TokenDialog
        channel={tokenFor}
        onOpenChange={(open) => !open && setTokenFor(null)}
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
                        {c.has_token ? (
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
                          variant={c.has_token ? 'outline' : 'default'}
                          onClick={() => setTokenFor(c)}
                          className="gap-1.5"
                        >
                          <Key className="size-4" />
                          {c.has_token ? t('channels.changeToken') : t('channels.connect')}
                        </Button>
                        {!c.has_token ? (
                          <span className="text-[11px] text-muted-foreground">
                            {t('channels.tokenNeeded')}
                          </span>
                        ) : null}
                      </div>
                    </div>

                    <Switch
                      checked={c.enabled}
                      disabled={busy === c.id || !c.has_token}
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
 * Token entry, in a dialog rather than inline. A secret field does not belong
 * sitting open on a page, and keeping it behind a button means the same form
 * serves both first connection and rotating a token later — which the inline
 * version could not do, since it disappeared once a token existed.
 */
function TokenDialog({
  channel,
  onOpenChange,
  onSaved,
}: {
  channel: Channel | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [token, setToken] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [saved, setSaved] = useState<{ name: string; handle: string } | null>(null)
  // The server reconnects the gateway itself; a restart is only needed when it
  // says so, so the hint has to follow the response rather than be assumed.
  const [needsRestart, setNeedsRestart] = useState(false)

  useEffect(() => {
    if (channel) {
      setToken('')
      setError(undefined)
      setSaved(null)
      setNeedsRestart(false)
      setReveal(false)
    }
  }, [channel])

  const save = async () => {
    if (!channel || !token.trim()) return
    setBusy(true)
    setError(undefined)
    try {
      const r = await post<{
        ok: boolean
        error?: string
        bot?: { name: string; handle: string }
        restart_required?: boolean
      }>(
        `/channels/${channel.id}/token`,
        { token: token.trim() },
      )
      if (!r.ok) {
        setError(r.error ?? t('channels.tokenRejected'))
        return
      }
      setSaved(r.bot ?? { name: '', handle: '' })
      setNeedsRestart(!!r.restart_required)
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const tokenURL = channel ? TOKEN_URLS[channel.id] : undefined

  return (
    <Dialog open={!!channel} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {channel?.has_token
              ? t('channels.changeTokenTitle', { channel: channel?.label ?? '' })
              : t('channels.connectTitle', { channel: channel?.label ?? '' })}
          </DialogTitle>
          <DialogDescription>{t('channels.tokenDialogDesc')}</DialogDescription>
        </DialogHeader>

        <DialogBody>
          {saved ? (
            // Naming the bot back is the only way to confirm the right token
            // was pasted — the string itself is unreadable.
            <div className="space-y-3">
              <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-[var(--success)]/40 bg-[color-mix(in_oklch,var(--success)_10%,transparent)] p-3 text-xs">
                <CheckCircle
                  className="mt-0.5 size-4 shrink-0 text-[var(--success)]"
                  weight="fill"
                />
                <span className="min-w-0">
                  {t('channels.tokenAccepted', {
                    bot: [saved.name, saved.handle].filter(Boolean).join(' ') || '—',
                  })}
                </span>
              </div>
              <p className="text-[11px] leading-relaxed text-muted-foreground">
                {needsRestart ? t('channels.restartHint') : t('channels.comingOnline')}
              </p>
            </div>
          ) : (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="channel-token">{t('channels.botToken')}</Label>
                <div className="flex gap-2">
                  <Input
                    id="channel-token"
                    type={reveal ? 'text' : 'password'}
                    autoFocus
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && save()}
                    placeholder={t('channels.tokenPlaceholder')}
                    autoComplete="off"
                  />
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={() => setReveal((v) => !v)}
                    aria-label={t('config.reveal')}
                    className="shrink-0"
                  >
                    {reveal ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
                  </Button>
                </div>
              </div>

              {tokenURL ? (
                <a
                  href={tokenURL}
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
            </>
          )}
        </DialogBody>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant={saved ? 'default' : 'outline'} size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          {!saved ? (
            <Button size="sm" onClick={save} loading={busy} disabled={!token.trim()}>
              {t('channels.verifyAndSave')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
