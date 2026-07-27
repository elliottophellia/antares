import { useEffect, useMemo, useState } from 'react'
import {
  ArrowClockwise,
  ArrowLeft,
  ArrowSquareOut,
  CaretRight,
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
 * Older configs stored labels like "Ollama (local)". Local endpoints are marked
 * with an icon now, so drop the suffix rather than reading it aloud in a
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
  // The picker is a dialog rather than a row of tabs: a tab strip grows with
  // every provider added and turns into a cramped horizontal scroll.
  const [picker, setPicker] = useState<{ open: boolean; connect?: ProviderInfo }>({ open: false })

  const activeProvider = provider || data?.active.provider || data?.providers[0]?.id || ''
  const current = data?.providers.find((p) => p.id === activeProvider)
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
      <ProviderDialog
        open={picker.open}
        connectTarget={picker.connect}
        providers={data?.providers ?? []}
        currentID={activeProvider}
        onOpenChange={(open) => setPicker((p) => ({ ...p, open }))}
        onPick={(id) => {
          setProvider(id)
          setFilter('')
        }}
        onConnected={() => {
          reload()
          modelsState.reload()
        }}
      />

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
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <CardTitle className="flex items-center gap-2">
                    <Cpu className="size-4 text-primary" weight="fill" />
                    {t('models.activeNow')}
                  </CardTitle>
                  <CardDescription>
                    {data.active.provider || t('common.notSet')} ·{' '}
                    {data.active.model || t('common.notSet')}
                  </CardDescription>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setPicker({ open: true })}
                  className="shrink-0 gap-1.5"
                >
                  <PlugsConnected className="size-4" />
                  {t('models.switchProvider')}
                </Button>
              </div>
            </CardHeader>
          </Card>

          {/* Which provider the list below belongs to, since there is no tab strip. */}
          {current ? (
            <button
              onClick={() => setPicker({ open: true })}
              className="flex w-full items-center gap-2 rounded-[var(--radius-sm)] border border-border bg-card px-3 py-2 text-left transition-colors hover:border-primary/40"
            >
              <span className="min-w-0 flex-1 truncate text-sm font-medium">
                {providerName(current.label)}
              </span>
              <ProviderStatus provider={current} />
              <CaretRight className="size-4 shrink-0 text-muted-foreground" />
            </button>
          ) : null}

          <div className="space-y-3">
            {modelsState.data?.unreachable ? (
              <ProviderUnreachable
                provider={current!}
                baseURL={modelsState.data.base_url ?? current?.base_url ?? ''}
                local={!!modelsState.data.local}
                onRetry={() => modelsState.reload()}
                retrying={modelsState.loading}
              />
            ) : modelsState.data?.needs_key ? (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Key className="size-4 text-primary" weight="fill" />
                    {t('models.connectTitle', { provider: providerName(current?.label ?? '') })}
                  </CardTitle>
                  <CardDescription>{t('models.connectDesc')}</CardDescription>
                </CardHeader>
                <CardContent>
                  <Button
                    size="sm"
                    onClick={() => setPicker({ open: true, connect: current })}
                    className="gap-1.5"
                  >
                    <Key className="size-4" />
                    {t('models.connect')}
                  </Button>
                </CardContent>
              </Card>
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
                      const isActive =
                        m.id === data.active.model && activeProvider === data.active.provider
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
          </div>
        </>
      )}
    </PageBody>
  )
}

/** One-glance state for a provider: keyed, local, or waiting for a key. */
function ProviderStatus({ provider }: { provider: ProviderInfo }) {
  const { t } = useI18n()
  if (provider.has_key) {
    return (
      <Badge variant="success" className="shrink-0">
        <CheckCircle className="size-3" weight="fill" />
        {t('models.connected')}
      </Badge>
    )
  }
  if (provider.local) {
    return (
      <Badge variant="outline" className="shrink-0">
        <Desktop className="size-3" />
        {t('models.localProvider')}
      </Badge>
    )
  }
  return (
    <Badge variant="warning" className="shrink-0">
      {t('models.needsKey')}
    </Badge>
  )
}

/**
 * Provider selection, and key entry for whichever one is picked, in a single
 * dialog. Two steps in one surface: the list stays searchable no matter how
 * many providers are configured, and connecting one never leaves the page.
 */
function ProviderDialog({
  open,
  providers,
  currentID,
  connectTarget,
  onOpenChange,
  onPick,
  onConnected,
}: {
  open: boolean
  providers: ProviderInfo[]
  currentID: string
  connectTarget?: ProviderInfo
  onOpenChange: (v: boolean) => void
  onPick: (id: string) => void
  onConnected: () => void
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [target, setTarget] = useState<ProviderInfo | null>(null)
  const [key, setKey] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  // Opening straight onto a provider's key form is what the "Connect" button on
  // the page means; opening bare starts at the list.
  useEffect(() => {
    if (open) {
      setQuery('')
      setTarget(connectTarget ?? null)
    }
  }, [open, connectTarget])

  // Stepping back and into a different provider must not carry a key across.
  useEffect(() => {
    setKey('')
    setError(undefined)
    setReveal(false)
  }, [target])

  const q = query.trim().toLowerCase()
  const shown = q
    ? providers.filter((p) => p.label.toLowerCase().includes(q) || p.id.toLowerCase().includes(q))
    : providers

  const choose = (p: ProviderInfo) => {
    // A provider with no key cannot list models, so go to the key step instead
    // of switching to a view that can only say "not connected".
    if (!p.has_key && !p.local) {
      setTarget(p)
      return
    }
    onPick(p.id)
    onOpenChange(false)
  }

  const connect = async () => {
    if (!target || !key.trim()) return
    setBusy(true)
    setError(undefined)
    try {
      const r = await post<{ ok: boolean; error?: string }>(
        `/providers/${encodeURIComponent(target.id)}/key`,
        { api_key: key.trim() },
      )
      if (!r.ok) {
        setError(r.error ?? t('models.connectFailed'))
        return
      }
      onConnected()
      onPick(target.id)
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const keyURL = target ? KEY_URLS[target.id] : undefined

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {target ? (
              <>
                <button
                  onClick={() => setTarget(null)}
                  aria-label={t('common.back')}
                  className="-ml-1 rounded p-1 text-muted-foreground transition-colors hover:text-foreground"
                >
                  <ArrowLeft className="size-4" />
                </button>
                {t('models.connectTitle', { provider: providerName(target.label) })}
              </>
            ) : (
              t('models.pickProvider')
            )}
          </DialogTitle>
          <DialogDescription>
            {target ? t('models.connectDesc') : t('models.pickProviderDesc')}
          </DialogDescription>
        </DialogHeader>

        <DialogBody>
          {target ? (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="provider-key">{t('setup.apiKey')}</Label>
                <div className="flex gap-2">
                  <Input
                    id="provider-key"
                    type={reveal ? 'text' : 'password'}
                    autoFocus
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

              {keyURL ? (
                <a
                  href={keyURL}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex items-center gap-1.5 text-xs text-primary underline underline-offset-2"
                >
                  {t('setup.getKey', { provider: providerName(target.label) })}
                  <ArrowSquareOut className="size-3.5" />
                </a>
              ) : null}

              {error ? <ProviderError message={error} /> : null}

              <p className="text-[11px] leading-relaxed text-muted-foreground">
                {t('models.connectEnvHint', { provider: target.id.toUpperCase() })}
              </p>
            </>
          ) : (
            <>
              {providers.length > 6 ? (
                <div className="relative">
                  <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    autoFocus
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder={t('models.searchProvider')}
                    className="pl-9"
                  />
                </div>
              ) : null}

              {shown.length === 0 ? (
                <p className="py-6 text-center text-xs text-muted-foreground">
                  {t('models.noProviderMatch')}
                </p>
              ) : (
                <div className="space-y-1.5">
                  {shown.map((p) => (
                    <div
                      key={p.id}
                      className={cn(
                        'flex items-center gap-2 rounded-[var(--radius-sm)] border p-2.5 transition-colors',
                        p.id === currentID
                          ? 'border-primary bg-primary/5'
                          : 'border-border hover:border-primary/40',
                      )}
                    >
                      <button onClick={() => choose(p)} className="min-w-0 flex-1 text-left">
                        <span className="block truncate text-sm font-medium">
                          {providerName(p.label)}
                        </span>
                        <span className="block truncate font-mono text-[10px] text-muted-foreground">
                          {p.base_url || p.kind}
                        </span>
                      </button>
                      <ProviderStatus provider={p} />
                      {/* A stored key can be wrong or rotated; without this the
                          only way to replace one is to edit the config file. */}
                      {p.has_key ? (
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          onClick={() => setTarget(p)}
                          aria-label={t('models.changeKey')}
                          className="shrink-0 text-muted-foreground"
                        >
                          <Key className="size-4" />
                        </Button>
                      ) : null}
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
        </DialogBody>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          {target ? (
            <Button size="sm" onClick={connect} loading={busy} disabled={!key.trim()}>
              {t('models.connect')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
          {t('models.unreachableTitle', { provider: providerName(provider?.label ?? '') })}
        </CardTitle>
        <CardDescription>
          {local
            ? t('models.unreachableLocal', {
                provider: providerName(provider?.label ?? ''),
                url: baseURL,
              })
            : t('models.unreachableRemote', { url: baseURL })}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-wrap items-center gap-3">
        <Button size="sm" variant="outline" onClick={onRetry} loading={retrying} className="gap-1.5">
          <ArrowClockwise className="size-4" />
          {t('models.retry')}
        </Button>
        <p className="text-[11px] text-muted-foreground">
          {t('models.unreachableHint', { provider: provider?.id ?? '' })}
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
