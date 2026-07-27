import { UsersThree, Warning } from '@phosphor-icons/react'
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

const CATEGORY_LABEL: Record<string, string> = {
  general: 'General',
  engineering: 'Engineering',
  research: 'Research',
  writing: 'Writing',
  security: 'Security — authorized testing',
}

export default function RolesPage() {
  const { t } = useI18n()
  const { data, loading } = useApi<{ roles: Role[]; scope?: string[] }>('/roles')

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
