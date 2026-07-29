import { useState } from 'react'
import { Toolbox } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { PageLayout } from '@/components/layout/PageLayout'
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  Switch,
} from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'
import { useI18n } from '@/lib/i18n'

interface ToolInfo {
  name: string
  description: string
  enabled: boolean
  requires_approval: boolean
  toolsets: string[]
}

interface ToolsResponse {
  toolset: string
  toolsets: string[]
  tools: ToolInfo[]
}

export default function ToolsPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ToolsResponse>('/tools')
  const [busy, setBusy] = useState('')

  const toggle = async (name: string, enabled: boolean) => {
    setBusy(name)
    try {
      await post('/tools/toggle', { name, enabled })
      reload()
    } finally {
      setBusy('')
    }
  }

  const setToolset = async (toolset: string) => {
    setBusy(toolset)
    try {
      await post('/tools/toolset', { toolset })
      reload()
    } finally {
      setBusy('')
    }
  }

  return (
    <PageLayout>
      {loading && !data ? (
        <SkeletonList count={8} />
      ) : !data ? (
        <EmptyState title={t('tools.loadFailed')} />
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle>{t('tools.activeToolset')}</CardTitle>
              <CardDescription>{t('tools.presetDesc')}</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap gap-2">
              {data.toolsets.map((preset) => (
                <button
                  key={preset}
                  disabled={!!busy}
                  onClick={() => setToolset(preset)}
                  className={
                    preset === data.toolset
                      ? 'rounded-full border border-primary bg-primary/15 px-3 py-1 text-xs font-medium text-primary'
                      : 'rounded-full border border-border px-3 py-1 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground'
                  }
                >
                  {preset}
                </button>
              ))}
            </CardContent>
          </Card>

          {data.tools.length === 0 ? (
            <EmptyState icon={<Toolbox className="size-8" />} title={t('tools.none')} />
          ) : (
            <div className="space-y-2">
              {data.tools.map((item) => (
                <Card key={item.name} className="flex items-start gap-3 p-3.5">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-xs font-medium">{item.name}</span>
                      {item.requires_approval ? <Badge variant="warning">{t('tools.mutates')}</Badge> : null}
                      {item.toolsets.map((ts) => (
                        <Badge key={ts} variant="outline">
                          {ts}
                        </Badge>
                      ))}
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">{item.description}</p>
                  </div>
                  <Switch
                    checked={item.enabled}
                    disabled={busy === item.name}
                    onCheckedChange={(v) => toggle(item.name, v)}
                    aria-label={`${t('common.enable')} ${item.name}`}
                  />
                </Card>
              ))}
            </div>
          )}
        </>
      )}
    </PageLayout>
  )
}
