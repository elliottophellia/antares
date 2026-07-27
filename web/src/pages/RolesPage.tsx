import { Lightning, Spinner, UsersThree, Warning } from '@phosphor-icons/react'
import { useEffect } from 'react'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { PageBody } from '@/components/layout/AppShell'
import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'

interface Role {
  name: string
  title: string
  summary: string
  category: string
  toolset: string
  model: string
  danger?: boolean
  source: string
}

interface ActiveAgent {
  id: string
  role: string
  task: string
  started_at: string
}

interface Perf {
  role: string
  score: number
  missions: number
  successes: number
  kept: number
}

const CATEGORY_LABEL: Record<string, string> = {
  general: 'General',
  engineering: 'Engineering',
  research: 'Research',
  writing: 'Writing',
  security: 'Security — authorized testing',
}

export default function RolesPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{
    roles: Role[]
    scope?: string[]
    active?: ActiveAgent[]
    performance?: Perf[]
  }>('/roles')

  // While sub-agents are running, refresh so the panel stays live.
  useEffect(() => {
    if (!data?.active?.length) return
    const t = setInterval(reload, 2000)
    return () => clearInterval(t)
  }, [data?.active?.length, reload])

  if (loading && !data) return <SkeletonList count={4} />

  const roles = data?.roles ?? []
  // Preserve the server's category order.
  const groups: { category: string; roles: Role[] }[] = []
  const index = new Map<string, number>()
  for (const r of roles) {
    if (!index.has(r.category)) {
      index.set(r.category, groups.length)
      groups.push({ category: r.category, roles: [] })
    }
    groups[index.get(r.category)!].roles.push(r)
  }

  return (
    <PageBody>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <UsersThree className="size-4 text-primary" weight="fill" />
            {t('roles.title')}
          </CardTitle>
          <CardDescription>{t('roles.intro')}</CardDescription>
        </CardHeader>
      </Card>

      {data?.active?.length ? (
        <Card className="border-primary/40">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Spinner className="size-4 animate-spin text-primary" />
              {t('roles.working', { n: data.active.length })}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {data.active.map((a) => (
              <div key={a.id} className="flex items-start gap-2 text-xs">
                <Badge variant="secondary" className="shrink-0">{a.role}</Badge>
                <span className="min-w-0 flex-1 truncate text-muted-foreground">{a.task}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}

      {data?.performance?.length ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Lightning className="size-4 text-primary" weight="fill" />
              {t('roles.performance')}
            </CardTitle>
            <CardDescription>{t('roles.performanceDesc')}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-1.5">
            {data.performance.map((p) => (
              <div key={p.role} className="flex items-center gap-3 text-xs">
                <span className="w-40 shrink-0 truncate font-medium">{p.role}</span>
                <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-primary"
                    style={{ width: `${Math.max(2, Math.min(100, p.score))}%` }}
                  />
                </div>
                <span className="w-10 shrink-0 text-right tabular-nums text-muted-foreground">
                  {p.score.toFixed(0)}
                </span>
                <span className="w-16 shrink-0 text-right text-[10px] text-muted-foreground">
                  {t('roles.missions', { n: p.missions })}
                </span>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}

      {groups.map((g) => (
        <div key={g.category} className="space-y-2">
          <h2 className="text-sm font-semibold">{CATEGORY_LABEL[g.category] ?? g.category}</h2>
          <div className="space-y-2">
            {g.roles.map((r) => (
              <Card key={r.name} className="p-3.5">
                <div className="flex items-start gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium">{r.title}</span>
                      <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                        {r.name}
                      </code>
                      {r.danger ? (
                        <Badge variant="warning">
                          <Warning className="size-3" weight="fill" />
                          {t('roles.authorized')}
                        </Badge>
                      ) : null}
                      {r.source === 'local' ? <Badge variant="secondary">{t('roles.custom')}</Badge> : null}
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">{r.summary}</p>
                    <div className="mt-1.5 flex flex-wrap gap-1.5">
                      {r.toolset ? <Badge variant="outline">{t('roles.tools', { set: r.toolset })}</Badge> : null}
                      {r.model ? <Badge variant="outline">{r.model}</Badge> : null}
                    </div>
                  </div>
                </div>
              </Card>
            ))}
          </div>
        </div>
      ))}

      <Card>
        <CardContent className="pt-4 text-xs text-muted-foreground">
          {t('roles.howto')}
        </CardContent>
      </Card>
    </PageBody>
  )
}
