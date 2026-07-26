import { useState } from 'react'
import { DiscordLogo, Plugs, TelegramLogo } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { PageBody } from '@/components/layout/AppShell'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  Input,
  Label,
  Switch,
} from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo } from '@/lib/i18n'

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

export default function ChannelsPage() {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const { data, loading, reload } = useApi<ChannelsResponse>('/channels')
  const [busy, setBusy] = useState('')
  const [tokens, setTokens] = useState<Record<string, string>>({})

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
                    <Icon className="mt-0.5 size-5 text-primary" weight="fill" />
                    <div className="min-w-0 flex-1">
                      <CardTitle className="flex items-center gap-2">
                        {c.label}
                        <Badge variant={c.connected ? 'success' : c.enabled ? 'warning' : 'outline'}>
                          {c.connected
                            ? t('channels.connected')
                            : c.enabled
                              ? t('channels.connecting')
                              : t('channels.disabled')}
                        </Badge>
                      </CardTitle>
                      <CardDescription>{c.detail}</CardDescription>
                    </div>
                    <Switch
                      checked={c.enabled}
                      disabled={busy === c.id || !c.has_token}
                      onCheckedChange={(v) => act(c.id, () => post(`/channels/${c.id}/toggle`, { enabled: v }))}
                      aria-label={`${t('common.enable')} ${c.label}`}
                    />
                  </div>
                </CardHeader>
                {!c.has_token ? (
                  <CardContent className="space-y-2">
                    <Label htmlFor={`tok-${c.id}`}>{t('channels.botToken')}</Label>
                    <div className="flex gap-2">
                      <Input
                        id={`tok-${c.id}`}
                        type="password"
                        value={tokens[c.id] ?? ''}
                        onChange={(e) => setTokens((t) => ({ ...t, [c.id]: e.target.value }))}
                        placeholder={t('channels.tokenPlaceholder')}
                        autoComplete="off"
                      />
                      <Button
                        variant="outline"
                        loading={busy === c.id}
                        disabled={!tokens[c.id]}
                        onClick={() =>
                          act(c.id, () => post(`/channels/${c.id}/token`, { token: tokens[c.id] }))
                        }
                      >
                        {t('common.save')}
                      </Button>
                    </div>
                  </CardContent>
                ) : null}
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
              <Card key={p.id} className="flex items-center gap-3 p-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline">{p.platform}</Badge>
                    <span className="truncate text-sm font-medium">{p.display_name || p.external_id}</span>
                    <Badge variant={p.status === 'approved' ? 'success' : 'warning'}>{p.status}</Badge>
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
