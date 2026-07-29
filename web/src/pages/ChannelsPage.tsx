import { useEffect, useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  ChatCircle,
  DiscordLogo,
  Eye,
  EyeSlash,
  Faders,
  GearSix,
  Key,
  PencilSimple,
  Plugs,
  Plus,
  SlackLogo,
  TelegramLogo,
  Trash,
  Warning,
  WhatsappLogo,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi, usePoll } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import {
  Badge,
  EmptyState,
  Input,
  Label,
  Switch,
  Tabs,
  TabsList,
  TabsTrigger,
  Textarea,
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
  bot_name?: string
  reply_style?: string
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
  // Poll so connect status and new pairing requests appear without a manual
  // refresh: connecting → connected, and a device awaiting approval, both land
  // within a few seconds. Polling pauses while the tab is hidden.
  const { data, loading, reload } = usePoll<ChannelsResponse>('/channels', 3000)
  const [busy, setBusy] = useState('')
  const [configFor, setConfigFor] = useState<Channel | null>(null)
  const [settingsFor, setSettingsFor] = useState<Channel | null>(null)
  const [tab, setTab] = useState<'channels' | 'devices'>('channels')

  const act = async (id: string, fn: () => Promise<unknown>) => {
    setBusy(id)
    try {
      await fn()
      reload()
    } finally {
      setBusy('')
    }
  }

  const channels = data?.channels ?? []
  const pairings = data?.pairings ?? []
  const pending = pairings.filter((p) => p.status === 'pending').length

  const header = (
    <Tabs value={tab} onValueChange={(v) => setTab(v as 'channels' | 'devices')}>
      <TabsList>
        <TabsTrigger value="channels">{t('channels.tabChannels')}</TabsTrigger>
        <TabsTrigger value="devices" className="gap-1.5">
          {t('channels.tabDevices')}
          {pending > 0 ? (
            <span className="rounded-full bg-[var(--warning)]/15 px-1.5 text-[10px] font-medium tabular-nums text-[var(--warning)]">
              {pending}
            </span>
          ) : null}
        </TabsTrigger>
      </TabsList>
    </Tabs>
  )

  return (
    <PageLayout header={header}>
      <ConfigDialog
        channel={configFor}
        onOpenChange={(open) => !open && setConfigFor(null)}
        onSaved={reload}
      />
      <ChannelSettingsDialog
        channel={settingsFor}
        onOpenChange={(open) => !open && setSettingsFor(null)}
      />

      {tab === 'devices' ? (
        <DevicesPanel loading={loading && !data} pairings={pairings} busy={busy} act={act} />
      ) : loading && !data ? (
        <SkeletonList count={4} />
      ) : (
        <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
          {channels.map((c) => {
            const Icon = ICONS[c.id] ?? Plugs
            const tone = c.connected
              ? 'text-[var(--success)]'
              : c.enabled
                ? 'text-[var(--warning)]'
                : 'text-muted-foreground'
            return (
              <div
                key={c.id}
                className="flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-3.5"
              >
                <div className="flex items-start gap-2.5">
                  <Icon className={cn('mt-0.5 size-5 shrink-0', tone)} weight="fill" />
                  <div className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{c.label}</span>
                    {c.connected && c.bot_name ? (
                      <p className="mt-0.5 truncate text-[11px] text-muted-foreground">{c.bot_name}</p>
                    ) : (
                      <p className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{c.detail}</p>
                    )}
                  </div>
                  <Switch
                    checked={c.enabled}
                    disabled={busy === c.id || !c.configured}
                    onCheckedChange={(v) =>
                      act(c.id, () => post(`/channels/${c.id}/toggle`, { enabled: v }))
                    }
                    aria-label={`${t('common.enable')} ${c.label}`}
                    className="shrink-0"
                  />
                </div>

                <div className="mb-3 mt-2.5 flex flex-wrap items-center gap-1.5">
                  <Badge variant={c.connected ? 'success' : c.enabled ? 'warning' : 'outline'}>
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
                </div>

                <div className="mt-auto flex flex-wrap items-center gap-x-2 gap-y-1.5 border-t border-border pt-3">
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
                    <span className="text-[11px] text-muted-foreground">{t('channels.tokenNeeded')}</span>
                  ) : null}
                  {c.configured && (c.id === 'discord' || c.id === 'telegram') ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setSettingsFor(c)}
                      className="gap-1.5"
                    >
                      <GearSix className="size-4" />
                      {t('channels.settings')}
                    </Button>
                  ) : null}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </PageLayout>
  )
}

function DevicesPanel({
  loading,
  pairings,
  busy,
  act,
}: {
  loading: boolean
  pairings: Pairing[]
  busy: string
  act: (id: string, fn: () => Promise<unknown>) => Promise<void>
}) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()

  if (loading) return <SkeletonList count={3} />
  if (pairings.length === 0) {
    return (
      <EmptyState
        icon={<Plugs className="size-8" />}
        title={t('channels.noDevices')}
        description={t('channels.noDevicesDesc')}
      />
    )
  }
  return (
    <div className="grid gap-2.5 sm:grid-cols-2">
      {pairings.map((p) => (
        <div
          key={p.id}
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-border bg-card p-3.5"
        >
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline">{p.platform}</Badge>
              <span className="truncate text-sm font-medium">{p.display_name || p.external_id}</span>
              <Badge variant={p.status === 'approved' ? 'success' : 'warning'}>{p.status}</Badge>
            </div>
            <p className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
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
        </div>
      ))}
    </div>
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

/* ---------- Per-channel settings (routing + commands) ---------- */

/**
 * Settings for a single Discord/Telegram channel: routing bindings scoped to
 * this platform, plus command registration. Opened from the channel card.
 */
function ChannelSettingsDialog({
  channel,
  onOpenChange,
}: {
  channel: Channel | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useI18n()
  const [subTab, setSubTab] = useState<'routing' | 'commands' | 'appearance'>('routing')
  const platform = channel?.id === 'telegram' ? 'telegram' : 'discord'
  const isDiscord = channel?.id === 'discord'

  useEffect(() => {
    if (channel) setSubTab('routing')
  }, [channel])

  return (
    <Dialog open={!!channel} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {t('channels.settingsTitle', { channel: channel?.label ?? '' })}
          </DialogTitle>
        </DialogHeader>
        <DialogBody>
          <Tabs value={subTab} onValueChange={(v) => setSubTab(v as typeof subTab)}>
            <TabsList className="mb-3">
              <TabsTrigger value="routing">{t('channels.tabRouting')}</TabsTrigger>
              <TabsTrigger value="commands">{t('channels.tabCommands')}</TabsTrigger>
              {isDiscord ? (
                <TabsTrigger value="appearance">{t('channels.tabAppearance')}</TabsTrigger>
              ) : null}
            </TabsList>
          </Tabs>
          {subTab === 'routing' ? (
            <RoutingPanel platform={platform} />
          ) : subTab === 'appearance' && channel ? (
            <AppearancePanel id={channel.id} current={channel.reply_style ?? 'embed'} />
          ) : channel ? (
            <CommandsPanel id={channel.id} />
          ) : null}
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

/**
 * Register / clear the bot's native commands (Discord slash commands, Telegram
 * menu). Moved here from the channel card.
 */
function CommandsPanel({ id }: { id: string }) {
  const { t } = useI18n()
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState('')

  const run = async (action: 'register' | 'clear') => {
    setBusy(action)
    setNote('')
    try {
      const r = await post<{ ok: boolean; error?: string }>(`/channels/${id}/commands/${action}`)
      setNote(
        r.ok
          ? action === 'clear'
            ? t('channels.cmdCleared')
            : t('channels.cmdRegistered')
          : r.error || t('channels.cmdFailed'),
      )
    } catch (e) {
      setNote((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            loading={busy === 'register'}
            disabled={busy === 'clear'}
            onClick={() => void run('register')}
            className="gap-1.5"
          >
            <Faders className="size-4" />
            {t('channels.cmdRegister')}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={busy !== ''}
            onClick={() => void run('clear')}
          >
            {t('channels.cmdClear')}
          </Button>
        </div>
        <p className="text-[11px] leading-relaxed text-muted-foreground">
          {t('channels.cmdRegisterHint')}
        </p>
        <p className="text-[11px] leading-relaxed text-muted-foreground">
          {t('channels.cmdClearHint')}
        </p>
      </div>
      {note ? <p className="text-xs text-muted-foreground">{note}</p> : null}
    </div>
  )
}

/**
 * Reply style: how Discord answers are rendered — a coloured embed card or a
 * plain message. Plain gets a blank first line so text sits under the bot name.
 */
function AppearancePanel({ id, current }: { id: string; current: string }) {
  const { t } = useI18n()
  const [style, setStyle] = useState(current === 'plain' ? 'plain' : 'embed')
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')

  const choose = async (next: 'plain' | 'embed') => {
    if (next === style) return
    setStyle(next)
    setBusy(true)
    setNote('')
    try {
      const r = await post<{ ok: boolean; error?: string }>(`/channels/${id}/style`, { style: next })
      setNote(r.ok ? t('channels.styleSaved') : r.error || t('channels.styleFailed'))
    } catch (e) {
      setNote((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const options: { value: 'plain' | 'embed'; label: string; desc: string }[] = [
    { value: 'embed', label: t('channels.styleEmbed'), desc: t('channels.styleEmbedDesc') },
    { value: 'plain', label: t('channels.stylePlain'), desc: t('channels.stylePlainDesc') },
  ]

  return (
    <div className="space-y-3">
      <Label>{t('channels.replyStyle')}</Label>
      <div className="grid gap-2 sm:grid-cols-2">
        {options.map((o) => (
          <button
            key={o.value}
            disabled={busy}
            onClick={() => void choose(o.value)}
            className={cn(
              'rounded-[var(--radius-md)] border p-3 text-left transition-colors',
              style === o.value ? 'border-primary bg-primary/8' : 'border-border hover:border-primary/40',
            )}
          >
            <div className="flex items-center gap-2 text-sm font-medium">
              {style === o.value ? <CheckCircle className="size-4 text-primary" weight="fill" /> : null}
              {o.label}
            </div>
            <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">{o.desc}</p>
          </button>
        ))}
      </div>
      {note ? <p className="text-xs text-muted-foreground">{note}</p> : null}
    </div>
  )
}

/* ---------- Routing (channel → agent bindings) ---------- */

interface Binding {
  id: string
  platform: 'discord' | 'telegram'
  guild_id: string
  channel_id: string
  label: string
  enabled: boolean
  role: string
  model: string
  toolset: string
  allowed_users: string[]
  reply_mode: string
  prompt_prefix: string
  relevance_filter: string
}

interface Role {
  name: string
  title: string
  summary: string
  category: string
  subrole?: boolean
}

interface ModelInfo {
  id: string
  name: string
  provider: string
  provider_label: string
}

const emptyBinding = (platform: 'discord' | 'telegram' = 'discord'): Binding => ({
  id: '',
  platform,
  guild_id: '',
  channel_id: '',
  label: '',
  enabled: true,
  role: '',
  model: '',
  toolset: '',
  allowed_users: [],
  reply_mode: 'mention',
  prompt_prefix: '',
  relevance_filter: '',
})

const selectClass =
  'flex h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm transition-colors focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50'

/**
 * Routing binds a specific chat channel to an agent role / model / toolset so
 * different rooms can behave differently. The list is a compact card grid; the
 * dialog does create and edit against POST /channels/bindings.
 */
function RoutingPanel({ platform }: { platform?: 'discord' | 'telegram' } = {}) {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{ bindings: Binding[] }>('/channels/bindings')
  const [editing, setEditing] = useState<Binding | null>(null)
  const [removing, setRemoving] = useState('')

  const allBindings = data?.bindings ?? []
  const bindings = platform ? allBindings.filter((b) => b.platform === platform) : allBindings

  const remove = async (b: Binding) => {
    if (!confirm(t('common.delete') + '?')) return
    setRemoving(b.id)
    try {
      await del(`/channels/bindings/${encodeURIComponent(b.id)}`)
      reload()
    } finally {
      setRemoving('')
    }
  }

  const toggle = async (b: Binding, enabled: boolean) => {
    await post('/channels/bindings', { ...b, enabled })
    reload()
  }

  if (loading && !data) return <SkeletonList count={3} />

  return (
    <div className="space-y-3">
      <BindingDialog
        binding={editing}
        lockedPlatform={platform}
        onOpenChange={(open) => !open && setEditing(null)}
        onSaved={reload}
      />

      <div className="flex justify-end">
        <Button size="sm" onClick={() => setEditing(emptyBinding(platform))} className="gap-1.5">
          <Plus className="size-4" />
          {t('channels.addBinding')}
        </Button>
      </div>

      {bindings.length === 0 ? (
        <EmptyState
          icon={<Faders className="size-8" />}
          title={t('channels.noBindings')}
          description={t('channels.noBindingsDesc')}
          action={
            <Button size="sm" onClick={() => setEditing(emptyBinding(platform))} className="gap-1.5">
              <Plus className="size-4" />
              {t('channels.addBinding')}
            </Button>
          }
        />
      ) : (
        <div className="grid gap-2.5 sm:grid-cols-2">
          {bindings.map((b) => {
            const Icon = ICONS[b.platform] ?? Plugs
            const title = b.label || b.channel_id || b.guild_id || b.platform
            return (
              <div
                key={b.id}
                className="flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-3.5"
              >
                <div className="flex items-start gap-2.5">
                  <Icon className="mt-0.5 size-5 shrink-0 text-muted-foreground" weight="fill" />
                  <div className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{title}</span>
                    <p className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
                      {b.guild_id ? `${b.guild_id} · ` : ''}
                      {b.channel_id || t('channels.bChannelAll')}
                    </p>
                  </div>
                  <Switch
                    checked={b.enabled}
                    onCheckedChange={(v) => void toggle(b, v)}
                    aria-label={t('channels.bEnabled')}
                    className="shrink-0"
                  />
                </div>

                <div className="mb-3 mt-2.5 flex flex-wrap items-center gap-1.5">
                  <Badge variant="outline">{b.platform}</Badge>
                  <Badge variant="secondary">{b.role || t('channels.bRoleDefault')}</Badge>
                  {b.model ? <Badge variant="outline">{b.model}</Badge> : null}
                </div>

                <div className="mt-auto flex items-center gap-x-2 border-t border-border pt-3">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setEditing(b)}
                    className="gap-1.5"
                  >
                    <PencilSimple className="size-4" />
                    {t('channels.editBinding')}
                  </Button>
                  <button
                    onClick={() => void remove(b)}
                    disabled={removing === b.id}
                    aria-label={t('common.delete')}
                    className="ml-auto shrink-0 text-muted-foreground transition-colors hover:text-destructive disabled:opacity-50"
                  >
                    <Trash className="size-4" />
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

interface DiscoverGuild {
  id: string
  name: string
  icon?: string
}
interface DiscoverChannel {
  id: string
  name: string
  type: number
  parent_id?: string
}

function BindingDialog({
  binding,
  lockedPlatform,
  onOpenChange,
  onSaved,
}: {
  binding: Binding | null
  lockedPlatform?: 'discord' | 'telegram'
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [form, setForm] = useState<Binding>(emptyBinding())
  const [allowedUsers, setAllowedUsers] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  // Discovery + pickers, loaded when the dialog opens.
  const [roles, setRoles] = useState<Role[]>([])
  const [toolsets, setToolsets] = useState<string[]>([])
  const [models, setModels] = useState<ModelInfo[]>([])
  const [guilds, setGuilds] = useState<DiscoverGuild[]>([])
  const [guildsError, setGuildsError] = useState<string>()
  const [channels, setChannels] = useState<DiscoverChannel[]>([])
  const [channelsError, setChannelsError] = useState<string>()

  const update = <K extends keyof Binding>(key: K, value: Binding[K]) =>
    setForm((f) => ({ ...f, [key]: value }))

  // Reset + load pickers whenever a binding is opened.
  useEffect(() => {
    if (!binding) return
    setForm(lockedPlatform ? { ...binding, platform: lockedPlatform } : binding)
    setAllowedUsers(binding.allowed_users.join(', '))
    setError(undefined)
    setChannels([])
    setChannelsError(undefined)

    void get<{ roles: Role[]; toolsets: string[] }>('/roles')
      .then((r) => {
        setRoles((r.roles ?? []).filter((role) => !role.subrole))
        setToolsets(r.toolsets ?? [])
      })
      .catch(() => {})
    void get<{ active: string; models: ModelInfo[] }>('/model/list-all')
      .then((r) => setModels(r.models ?? []))
      .catch(() => {})
  }, [binding])

  // Discord guild discovery.
  useEffect(() => {
    if (!binding || form.platform !== 'discord') return
    void get<{ guilds: DiscoverGuild[]; error?: string }>('/channels/discord/guilds')
      .then((r) => {
        setGuilds(r.guilds ?? [])
        setGuildsError(r.error)
      })
      .catch((e: Error) => setGuildsError(e.message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [binding, form.platform])

  // Discord channel discovery, once a guild is picked.
  useEffect(() => {
    if (!binding || form.platform !== 'discord' || !form.guild_id) {
      setChannels([])
      return
    }
    setChannelsError(undefined)
    void get<{ channels: DiscoverChannel[]; error?: string }>(
      `/channels/discord/guilds/${encodeURIComponent(form.guild_id)}/channels`,
    )
      .then((r) => {
        setChannels(r.channels ?? [])
        setChannelsError(r.error)
      })
      .catch((e: Error) => setChannelsError(e.message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [binding, form.platform, form.guild_id])

  const save = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const body: Binding = {
        ...form,
        allowed_users: allowedUsers
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
      }
      const r = await post<{ ok: boolean; error?: string; binding?: Binding }>(
        '/channels/bindings',
        body,
      )
      if (!r.ok) {
        setError(r.error ?? t('channels.discoverFailed'))
        return
      }
      onSaved()
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const valid =
    form.platform === 'telegram' ? !!form.channel_id.trim() : !!form.guild_id.trim()

  return (
    <Dialog open={!!binding} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {binding?.id ? t('channels.editBinding') : t('channels.addBinding')}
          </DialogTitle>
        </DialogHeader>
        <DialogBody>
          {/* Platform */}
          {lockedPlatform ? null : (
            <div className="grid gap-1.5">
              <Label>{t('channels.bPlatform')}</Label>
              <div className="inline-flex w-fit rounded-[var(--radius-sm)] border border-border p-0.5">
                {(['discord', 'telegram'] as const).map((p) => (
                  <button
                    key={p}
                    onClick={() => update('platform', p)}
                    className={cn(
                      'rounded-[calc(var(--radius-sm)-2px)] px-3 py-1 text-xs font-medium transition-colors',
                      form.platform === p
                        ? 'bg-primary text-primary-foreground'
                        : 'text-muted-foreground hover:text-foreground',
                    )}
                  >
                    {p}
                  </button>
                ))}
              </div>
            </div>
          )}

          {form.platform === 'discord' ? (
            <>
              <div className="grid gap-1.5">
                <Label htmlFor="b-server">{t('channels.bServer')}</Label>
                <select
                  id="b-server"
                  className={selectClass}
                  value={form.guild_id}
                  onChange={(e) => {
                    update('guild_id', e.target.value)
                    update('channel_id', '')
                  }}
                >
                  <option value="">—</option>
                  {guilds.map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                </select>
                {guildsError ? (
                  <p className="text-[11px] text-destructive">{guildsError}</p>
                ) : null}
              </div>

              <div className="grid gap-1.5">
                <Label htmlFor="b-channel">{t('channels.bChannel')}</Label>
                <select
                  id="b-channel"
                  className={selectClass}
                  value={form.channel_id}
                  onChange={(e) => update('channel_id', e.target.value)}
                  disabled={!form.guild_id}
                >
                  <option value="">{t('channels.bChannelAll')}</option>
                  {channels.map((c) => (
                    <option key={c.id} value={c.id}>
                      #{c.name}
                    </option>
                  ))}
                </select>
                {channelsError ? (
                  <p className="text-[11px] text-destructive">{channelsError}</p>
                ) : null}
              </div>
            </>
          ) : (
            <div className="grid gap-1.5">
              <Label htmlFor="b-chatid">{t('channels.bChatId')}</Label>
              <Input
                id="b-chatid"
                value={form.channel_id}
                onChange={(e) => update('channel_id', e.target.value)}
                placeholder="123456789"
                className="font-mono text-xs"
              />
              <p className="text-[11px] text-muted-foreground">{t('channels.bChatIdHint')}</p>
            </div>
          )}

          {/* Label */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-label">{t('channels.bLabel')}</Label>
            <Input
              id="b-label"
              value={form.label}
              onChange={(e) => update('label', e.target.value)}
            />
          </div>

          {/* Role */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-role">{t('channels.bRole')}</Label>
            <select
              id="b-role"
              className={selectClass}
              value={form.role}
              onChange={(e) => update('role', e.target.value)}
            >
              <option value="">{t('channels.bRoleDefault')}</option>
              {roles.map((r) => (
                <option key={r.name} value={r.name}>
                  {r.title || r.name}
                </option>
              ))}
            </select>
          </div>

          {/* Model */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-model">{t('channels.bModel')}</Label>
            <select
              id="b-model"
              className={selectClass}
              value={form.model}
              onChange={(e) => update('model', e.target.value)}
            >
              <option value="">{t('channels.bModelDefault')}</option>
              {models.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name} — {m.provider_label}
                </option>
              ))}
            </select>
          </div>

          {/* Toolset */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-toolset">{t('channels.bToolset')}</Label>
            <select
              id="b-toolset"
              className={selectClass}
              value={form.toolset}
              onChange={(e) => update('toolset', e.target.value)}
            >
              <option value="">{t('channels.bToolsetDefault')}</option>
              {toolsets.map((ts) => (
                <option key={ts} value={ts}>
                  {ts}
                </option>
              ))}
            </select>
          </div>

          {/* Reply mode */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-reply">{t('channels.bReplyMode')}</Label>
            <select
              id="b-reply"
              className={selectClass}
              value={form.reply_mode || 'mention'}
              onChange={(e) => update('reply_mode', e.target.value)}
            >
              <option value="mention">mention</option>
              <option value="always">always</option>
            </select>
          </div>

          {/* Allowed users */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-users">{t('channels.bAllowedUsers')}</Label>
            <Input
              id="b-users"
              value={allowedUsers}
              onChange={(e) => setAllowedUsers(e.target.value)}
              className="font-mono text-xs"
            />
            <p className="text-[11px] text-muted-foreground">{t('channels.bAllowedUsersHint')}</p>
          </div>

          {/* Prompt prefix */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-prefix">{t('channels.bPromptPrefix')}</Label>
            <Textarea
              id="b-prefix"
              rows={2}
              value={form.prompt_prefix}
              onChange={(e) => update('prompt_prefix', e.target.value)}
            />
            <p className="text-[11px] text-muted-foreground">{t('channels.bPromptPrefixHint')}</p>
          </div>

          {/* Relevance filter */}
          <div className="grid gap-1.5">
            <Label htmlFor="b-relevance">{t('channels.bRelevance')}</Label>
            <Textarea
              id="b-relevance"
              rows={2}
              value={form.relevance_filter}
              onChange={(e) => update('relevance_filter', e.target.value)}
            />
            <p className="text-[11px] text-muted-foreground">{t('channels.bRelevanceHint')}</p>
          </div>

          {/* Enabled */}
          <div className="flex items-center justify-between">
            <Label htmlFor="b-enabled">{t('channels.bEnabled')}</Label>
            <Switch
              id="b-enabled"
              checked={form.enabled}
              onCheckedChange={(v) => update('enabled', v)}
            />
          </div>

          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button size="sm" disabled={!valid} loading={busy} onClick={() => void save()}>
            {t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
