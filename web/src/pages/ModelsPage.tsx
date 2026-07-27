import { useEffect, useMemo, useState } from 'react'
import {
  ArrowClockwise,
  ArrowSquareOut,
  CheckCircle,
  Cpu,
  Desktop,
  Eye,
  EyeSlash,
  Key,
  Lightning,
  MagnifyingGlass,
  PlugsConnected,
  Warning,
  Wrench,
} from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
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
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'

interface ModelInfo {
  id: string
  name: string
  provider: string
  context_window: number
  max_output: number
  input_cost: number
  output_cost: number
  vision: boolean
  tools: boolean
  reasoning: boolean
}

interface ProviderInfo {
  id: string
  label: string
  kind: string
  enabled: boolean
  has_key: boolean
  local: boolean
  base_url: string
  active: boolean
}

interface ModelsResponse {
  active: { model: string; provider: string }
  providers: ProviderInfo[]
}

interface ModelListResponse {
  models: ModelInfo[]
  error?: string
  needs_key?: boolean
  unreachable?: boolean
  local?: boolean
  base_url?: string
}

/**
 * Older configs stored labels like "Ollama (local)". The tab now marks a local
 * endpoint with an icon, so drop the suffix rather than reading it aloud in a
 * sentence like "Ollama (local) is not running".
 */
function providerName(label: string): string {
  return label.replace(/\s*\((local|lokal)\)\s*$/i, '').trim()
}

/** Where to get a key, per provider. Saves a search when one is missing. */
const KEY_URLS: Record<string, string> = {
  openrouter: 'https://openrouter.ai/keys',
  anthropic: 'https://console.anthropic.com/settings/keys',
  openai: 'https://platform.openai.com/api-keys',
  gemini: 'https://aistudio.google.com/apikey',
}

export default function ModelsPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ModelsResponse>('/model/options')
  const [provider, setProvider] = useState<string>('')
  const [filter, setFilter] = useState('')
  const [saving, setSaving] = useState('')

  const activeProvider = provider || data?.active.provider || data?.providers[0]?.id || ''
  const modelsState = useApi<ModelListResponse>(
    activeProvider ? `/model/list?provider=${encodeURIComponent(activeProvider)}` : null,
    [activeProvider],
  )

  // Aggregators publish hundreds of models; rendering them all produced a page
  // tens of thousands of pixels tall. Cap the list and let search reach the rest.
  const MAX_ROWS = 25
  const { models, total } = useMemo(() => {
    const list = modelsState.data?.models ?? []
    const q = filter.trim().toLowerCase()
    const matched = q
      ? list.filter((m) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q))
      : list
    return { models: matched.slice(0, MAX_ROWS), total: matched.length }
  }, [modelsState.data, filter])

  const selectModel = async (id: string) => {
    setSaving(id)
    try {
      await post('/model/set', { model: id, provider: activeProvider })
      reload()
    } finally {
      setSaving('')
    }
  }

  return (
    <PageBody>
      {loading && !data ? (
        <div className="space-y-4">
          <Skeleton className="h-9 w-full max-w-md" />
          <SkeletonList count={5} />
        </div>
      ) : !data ? (
        <EmptyState title={t('models.loadProvidersFailed')} description={t('models.checkBackend')} />
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Cpu className="size-4 text-primary" weight="fill" />
                {t('models.activeNow')}
              </CardTitle>
              <CardDescription>
                {data.active.provider || t('common.notSet')} ·{' '}
                {data.active.model || t('common.notSet')}
              </CardDescription>
            </CardHeader>
          </Card>

          <Tabs value={activeProvider} onValueChange={setProvider}>
            <TabsList className="w-full justify-start overflow-x-auto">
              {data.providers.map((p) => (
                <TabsTrigger key={p.id} value={p.id} className="gap-1.5">
                  {providerName(p.label)}
                  {p.has_key ? (
                    <CheckCircle className="size-3 text-[var(--success)]" weight="fill" />
                  ) : p.local ? (
                    <Desktop className="size-3 text-muted-foreground" />
                  ) : null}
                </TabsTrigger>
              ))}
            </TabsList>

            {data.providers.map((p) => (
              <TabsContent key={p.id} value={p.id} className="space-y-3">
                {modelsState.data?.unreachable ? (
                  <ProviderUnreachable
                    provider={p}
                    baseURL={modelsState.data.base_url ?? p.base_url}
                    local={!!modelsState.data.local}
                    onRetry={() => modelsState.reload()}
                    retrying={modelsState.loading}
                  />
                ) : modelsState.data?.needs_key ? (
                  // No credential: connecting here beats sending the user to
                  // hunt for the field in Settings.
                  <ConnectProvider
                    provider={p}
                    onConnected={() => {
                      reload()
                      modelsState.reload()
                    }}
                  />
                ) : (
                  <>
                    <div className="relative">
                      <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                      <Input
                        value={filter}
                        onChange={(e) => setFilter(e.target.value)}
                        placeholder={t('models.searchModel')}
                        className="pl-9"
                      />
                    </div>

                    {modelsState.loading ? (
                      <SkeletonList count={6} />
                    ) : modelsState.data?.error ? (
                      <ProviderError message={modelsState.data.error} />
                    ) : models.length === 0 ? (
                      <EmptyState title={t('models.none')} description={t('models.noneDesc')} />
                    ) : (
                      <div className="space-y-2">
                        {total > models.length ? (
                          <p className="text-xs text-muted-foreground">
                            {t('models.showingOf', { shown: models.length, total })}
                          </p>
                        ) : null}
                        {models.map((m) => {
                          const isActive = m.id === data.active.model && p.id === data.active.provider
                          return (
                            <Card
                              key={m.id}
                              className={cn(
                                'flex items-center gap-3 p-3.5 transition-colors',
                                isActive ? 'border-primary' : 'hover:border-primary/40',
                              )}
                            >
                              <div className="min-w-0 flex-1">
                                <p className="truncate text-sm font-medium">{m.name}</p>
                                <p className="truncate font-mono text-[11px] text-muted-foreground">
                                  {m.id}
                                </p>
                                <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                                  {m.context_window > 0 ? (
                                    <Badge variant="outline">
                                      {t('models.ctx', { n: Math.round(m.context_window / 1000) })}
                                    </Badge>
                                  ) : null}
                                  {m.input_cost > 0 ? (
                                    <Badge variant="outline">
                                      ${m.input_cost.toFixed(2)}/${m.output_cost.toFixed(2)}{' '}
                                      {t('models.per1M')}
                                    </Badge>
                                  ) : null}
                                  {m.vision ? (
                                    <Badge variant="secondary" className="hidden sm:inline-flex">
                                      <Eye className="size-3" /> {t('models.vision')}
                                    </Badge>
                                  ) : null}
                                  {m.tools ? (
                                    <Badge variant="secondary" className="hidden sm:inline-flex">
                                      <Wrench className="size-3" /> {t('models.tools')}
                                    </Badge>
                                  ) : null}
                                  {m.reasoning ? (
                                    <Badge variant="secondary" className="hidden sm:inline-flex">
                                      <Lightning className="size-3" /> {t('models.reasoning')}
                                    </Badge>
                                  ) : null}
                                </div>
                              </div>
                              <Button
                                size="sm"
                                variant={isActive ? 'secondary' : 'outline'}
                                disabled={isActive}
                                loading={saving === m.id}
                                onClick={() => selectModel(m.id)}
                                className="shrink-0"
                              >
                                {isActive ? t('common.active') : t('common.use')}
                              </Button>
                            </Card>
                          )
                        })}
                      </div>
                    )}
                  </>
                )}
              </TabsContent>
            ))}
          </Tabs>
        </>
      )}
    </PageBody>
  )
}

/**
 * Inline credential form for a provider with no key. Verifies against the
 * provider before saving, so a typo is caught here rather than on the next turn.
 */
function ConnectProvider({
  provider,
  onConnected,
}: {
  provider: ProviderInfo
  onConnected: () => void
}) {
  const { t } = useI18n()
  const [key, setKey] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  // Switching provider tabs must not carry a half-typed key across.
  useEffect(() => {
    setKey('')
    setError(undefined)
  }, [provider.id])

  const connect = async () => {
    if (!key.trim()) return
    setBusy(true)
    setError(undefined)
    try {
      const r = await post<{ ok: boolean; error?: string }>(
        `/providers/${encodeURIComponent(provider.id)}/key`,
        { api_key: key.trim() },
      )
      if (!r.ok) {
        setError(r.error ?? t('models.connectFailed'))
        return
      }
      setKey('')
      onConnected()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const keyURL = KEY_URLS[provider.id]

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Key className="size-4 text-primary" weight="fill" />
          {t('models.connectTitle', { provider: providerName(provider.label) })}
        </CardTitle>
        <CardDescription>{t('models.connectDesc')}</CardDescription>
      </CardHeader>

      <CardContent className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor={`key-${provider.id}`}>{t('setup.apiKey')}</Label>
          <div className="flex gap-2">
            <Input
              id={`key-${provider.id}`}
              type={reveal ? 'text' : 'password'}
              value={key}
              onChange={(e) => setKey(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && connect()}
              placeholder="sk-…"
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

        {error ? <ProviderError message={error} /> : null}

        <div className="flex flex-wrap items-center gap-3">
          <Button size="sm" onClick={connect} loading={busy} disabled={!key.trim()}>
            {t('models.connect')}
          </Button>
          {keyURL ? (
            <a
              href={keyURL}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1.5 text-xs text-primary underline underline-offset-2"
            >
              {t('setup.getKey', { provider: providerName(provider.label) })}
              <ArrowSquareOut className="size-3.5" />
            </a>
          ) : null}
        </div>

        <p className="text-[11px] leading-relaxed text-muted-foreground">
          {t('models.connectEnvHint', { provider: provider.id.toUpperCase() })}
        </p>
      </CardContent>
    </Card>
  )
}

/**
 * Shown when the endpoint refuses the connection. For a local runtime that
 * almost always means it simply is not running, which is advice, not an error.
 */
function ProviderUnreachable({
  provider,
  baseURL,
  local,
  onRetry,
  retrying,
}: {
  provider: ProviderInfo
  baseURL: string
  local: boolean
  onRetry: () => void
  retrying: boolean
}) {
  const { t } = useI18n()
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <PlugsConnected className="size-4 text-muted-foreground" />
          {t('models.unreachableTitle', { provider: providerName(provider.label) })}
        </CardTitle>
        <CardDescription>
          {local
            ? t('models.unreachableLocal', { provider: providerName(provider.label), url: baseURL })
            : t('models.unreachableRemote', { url: baseURL })}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-wrap items-center gap-3">
        <Button size="sm" variant="outline" onClick={onRetry} loading={retrying} className="gap-1.5">
          <ArrowClockwise className="size-4" />
          {t('models.retry')}
        </Button>
        <p className="text-[11px] text-muted-foreground">
          {t('models.unreachableHint', { provider: provider.id })}
        </p>
      </CardContent>
    </Card>
  )
}

function ProviderError({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
      <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
      <span className="min-w-0 break-words">{message}</span>
    </div>
  )
}
