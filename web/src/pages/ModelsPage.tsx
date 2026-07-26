import { useMemo, useState } from 'react'
import { CheckCircle, Cpu, Eye, Lightning, MagnifyingGlass, Wrench } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
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
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { useI18n } from '@/lib/i18n'

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
  base_url: string
  active: boolean
}

interface ModelsResponse {
  active: { model: string; provider: string }
  providers: ProviderInfo[]
}

export default function ModelsPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ModelsResponse>('/model/options')
  const [provider, setProvider] = useState<string>('')
  const [filter, setFilter] = useState('')
  const [saving, setSaving] = useState('')

  const activeProvider = provider || data?.active.provider || data?.providers[0]?.id || ''
  const modelsState = useApi<{ models: ModelInfo[]; error?: string }>(
    activeProvider ? `/model/list?provider=${encodeURIComponent(activeProvider)}` : null,
    [activeProvider],
  )

  const models = useMemo(() => {
    const list = modelsState.data?.models ?? []
    const q = filter.trim().toLowerCase()
    if (!q) return list
    return list.filter((m) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q))
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
                {data.active.provider || t('common.notSet')} · {data.active.model || t('common.notSet')}
              </CardDescription>
            </CardHeader>
          </Card>

          <Tabs value={activeProvider} onValueChange={setProvider}>
            <TabsList className="w-full justify-start overflow-x-auto">
              {data.providers.map((p) => (
                <TabsTrigger key={p.id} value={p.id} className="gap-1.5">
                  {p.label}
                  {p.has_key ? (
                    <CheckCircle className="size-3 text-[var(--success)]" weight="fill" />
                  ) : null}
                </TabsTrigger>
              ))}
            </TabsList>

            {data.providers.map((p) => (
              <TabsContent key={p.id} value={p.id} className="space-y-3">
                {!p.has_key ? (
                  <Card className="border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_10%,transparent)]">
                    <CardContent className="pt-4 text-xs">
                      {t('models.noKey', { provider: p.label })}{' '}
                      (<code className="font-mono">providers.{p.id}.api_key</code>){' '}
                      {t('models.noKeyPath')}
                    </CardContent>
                  </Card>
                ) : null}

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
                  <EmptyState title={t('models.loadFailed')} description={modelsState.data.error} />
                ) : models.length === 0 ? (
                  <EmptyState
                    title={t('models.none')}
                    description={t('models.noneDesc')}
                  />
                ) : (
                  <div className="space-y-2">
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
                            <p className="truncate font-mono text-[11px] text-muted-foreground">{m.id}</p>
                            <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                              {m.context_window > 0 ? (
                                <Badge variant="outline">
                                  {t('models.ctx', { n: Math.round(m.context_window / 1000) })}
                                </Badge>
                              ) : null}
                              {m.vision ? (
                                <Badge variant="secondary">
                                  <Eye className="size-3" /> {t('models.vision')}
                                </Badge>
                              ) : null}
                              {m.tools ? (
                                <Badge variant="secondary">
                                  <Wrench className="size-3" /> {t('models.tools')}
                                </Badge>
                              ) : null}
                              {m.reasoning ? (
                                <Badge variant="secondary">
                                  <Lightning className="size-3" /> {t('models.reasoning')}
                                </Badge>
                              ) : null}
                              {m.input_cost > 0 ? (
                                <Badge variant="outline">
                                  ${m.input_cost.toFixed(2)}/${m.output_cost.toFixed(2)} {t('models.per1M')}
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
              </TabsContent>
            ))}
          </Tabs>
        </>
      )}
    </PageBody>
  )
}
