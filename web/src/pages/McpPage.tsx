import { useState } from 'react'
import { CaretDown, CheckCircle, PlugsConnected, Storefront, XCircle } from '@phosphor-icons/react'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { usePageActions } from '@/components/layout/PageChrome'
import { HubDialog } from '@/components/hub/HubDialog'

interface McpTool {
  name: string
  description: string
}

interface McpServer {
  name: string
  connected: boolean
  error?: string
  tools: McpTool[]
}

export default function McpPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{ enabled: boolean; servers: McpServer[] }>('/mcp')
  const [open, setOpen] = useState<string | null>(null)
  const [browsing, setBrowsing] = useState(false)
  const [tab, setTab] = useState<'servers' | 'docs'>('servers')

  usePageActions(
    <Button size="sm" onClick={() => setBrowsing(true)} className="gap-1.5">
      <Storefront className="size-4" />
      {t('hub.browse')}
    </Button>,
    [t],
  )

  if (loading && !data) return <SkeletonList count={3} />

  const servers = data?.servers ?? []

  const header = (
    <Tabs value={tab} onValueChange={(v) => setTab(v as 'servers' | 'docs')}>
      <TabsList>
        <TabsTrigger value="servers">{t('mcp.tabServers')}</TabsTrigger>
        <TabsTrigger value="docs">{t('mcp.tabDocs')}</TabsTrigger>
      </TabsList>
    </Tabs>
  )

  return (
    <PageLayout header={header}>
      <HubDialog kind="mcp" open={browsing} onOpenChange={setBrowsing} onInstalled={reload} />

      {tab === 'docs' ? (
        <McpDocs />
      ) : (
        <>
          {!data?.enabled ? (
            <Card className="border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_10%,transparent)]">
              <CardContent className="pt-4 text-xs sm:text-sm">{t('mcp.disabled')}</CardContent>
            </Card>
          ) : null}

          {servers.length === 0 ? (
            <EmptyState
              icon={<PlugsConnected className="size-8" />}
              title={t('mcp.none')}
              description={t('mcp.noneDesc')}
              action={
                <Button size="sm" onClick={() => setBrowsing(true)} className="gap-1.5">
                  <Storefront className="size-4" />
                  {t('hub.browse')}
                </Button>
              }
            />
          ) : (
            <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
              {servers.map((s) => (
                <div
                  key={s.name}
                  className="flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-3.5"
                >
                  <div className="flex items-start gap-2">
                    {s.connected ? (
                      <CheckCircle className="mt-0.5 size-4 shrink-0 text-[var(--success)]" weight="fill" />
                    ) : (
                      <XCircle className="mt-0.5 size-4 shrink-0 text-destructive" weight="fill" />
                    )}
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">{s.name}</span>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    <Badge variant={s.connected ? 'success' : 'destructive'}>
                      {s.connected ? t('mcp.connected') : t('mcp.failed')}
                    </Badge>
                    {s.connected ? (
                      <Badge variant="outline">{t('mcp.toolCount', { n: s.tools.length })}</Badge>
                    ) : null}
                  </div>
                  {s.error ? (
                    <p className="mt-1.5 break-words text-[11px] text-destructive">{s.error}</p>
                  ) : null}
                  {s.tools.length > 0 ? (
                    <div className="mt-2.5 border-t border-border pt-2">
                      <button
                        onClick={() => setOpen(open === s.name ? null : s.name)}
                        className="flex w-full items-center gap-1.5 text-xs font-medium text-muted-foreground"
                      >
                        {t('mcp.showTools')}
                        <CaretDown
                          className={cn('size-3 transition-transform', open === s.name && 'rotate-180')}
                        />
                      </button>
                      {open === s.name ? (
                        <div className="mt-2 space-y-1.5">
                          {s.tools.map((tool) => (
                            <div key={tool.name} className="rounded-[var(--radius-sm)] border border-border p-2">
                              <p className="break-all font-mono text-[11px] font-medium">{tool.name}</p>
                              {tool.description ? (
                                <p className="mt-0.5 text-[11px] text-muted-foreground">{tool.description}</p>
                              ) : null}
                            </div>
                          ))}
                        </div>
                      ) : null}
                    </div>
                  ) : null}
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </PageLayout>
  )
}

function McpDocs() {
  const { t } = useI18n()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('mcp.howto')}</CardTitle>
        <CardDescription>{t('mcp.howtoDesc')}</CardDescription>
      </CardHeader>
      <CardContent>
        <pre className="overflow-x-auto rounded-[var(--radius-sm)] bg-muted/50 p-3 font-mono text-[11px] leading-relaxed">
{`mcp:
  enabled: true
  servers:
    filesystem:
      enabled: true
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/you/data"]
    remote:
      enabled: true
      transport: http
      url: https://example.com/mcp
      headers:
        Authorization: "Bearer …"`}
        </pre>
        <p className="mt-3 text-xs text-muted-foreground">{t('mcp.docsHint')}</p>
      </CardContent>
    </Card>
  )
}
