import { useState } from 'react'
import { ArrowClockwise, PuzzlePiece, Warning } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
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

interface Plugin {
  name: string
  description: string
  version: string
  command: string
  hooks?: string[]
  dir: string
  enabled: boolean
  error?: string
}

export default function PluginsPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{
    enabled: boolean
    plugins: Plugin[]
    dirs?: string[]
  }>('/plugins')
  const [busy, setBusy] = useState('')

  usePageActions(
    <Button size="sm" variant="outline" onClick={() => void refresh()} className="gap-1.5">
      <ArrowClockwise className="size-4" />
      {t('plugins.rescan')}
    </Button>,
    [t],
  )

  const refresh = async () => {
    setBusy('*')
    try {
      await post('/plugins/reload')
      reload()
    } finally {
      setBusy('')
    }
  }

  const toggle = async (name: string, enabled: boolean) => {
    setBusy(name)
    try {
      await post(`/plugins/${encodeURIComponent(name)}/toggle`, { enabled })
      reload()
    } finally {
      setBusy('')
    }
  }

  if (loading && !data) return <SkeletonList count={3} />

  const plugins = data?.plugins ?? []

  return (
    <PageLayout>
      {!data?.enabled ? (
        <Card className="border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_10%,transparent)]">
          <CardContent className="pt-4 text-xs sm:text-sm">{t('plugins.disabled')}</CardContent>
        </Card>
      ) : null}

      {plugins.length === 0 ? (
        <EmptyState
          icon={<PuzzlePiece className="size-8" />}
          title={t('plugins.none')}
          description={t('plugins.noneDesc', { dir: data?.dirs?.[0] ?? '~/.antares/plugins' })}
        />
      ) : (
        <div className="space-y-3">
          {plugins.map((p) => (
            <Card key={p.name}>
              <CardHeader>
                <div className="flex items-start gap-3">
                  <PuzzlePiece
                    className={p.error ? 'mt-0.5 size-5 shrink-0 text-destructive' : 'mt-0.5 size-5 shrink-0 text-primary'}
                    weight="fill"
                  />
                  <div className="min-w-0 flex-1">
                    <CardTitle className="flex flex-wrap items-center gap-2">
                      {p.name}
                      {p.version ? <Badge variant="outline">{p.version}</Badge> : null}
                      {p.error ? (
                        <Badge variant="destructive">
                          <Warning className="size-3" weight="fill" />
                          {t('plugins.broken')}
                        </Badge>
                      ) : null}
                      {p.hooks?.map((h) => (
                        <Badge key={h} variant="secondary" className="font-mono text-[10px]">
                          {h}
                        </Badge>
                      ))}
                    </CardTitle>
                    {p.description ? <CardDescription>{p.description}</CardDescription> : null}
                    {p.error ? (
                      <CardDescription className="break-words text-destructive">
                        {p.error}
                      </CardDescription>
                    ) : null}
                    <p className="mt-1 break-all font-mono text-[10px] text-muted-foreground">
                      {p.dir}
                      {p.command ? ` · ${p.command}` : ''}
                    </p>
                  </div>
                  {!p.error ? (
                    <Switch
                      checked={p.enabled}
                      disabled={busy === p.name || busy === '*'}
                      onCheckedChange={(v) => toggle(p.name, v)}
                      aria-label={`${t('common.enable')} ${p.name}`}
                      className="mt-1 shrink-0"
                    />
                  ) : null}
                </div>
              </CardHeader>
            </Card>
          ))}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>{t('plugins.howto')}</CardTitle>
          <CardDescription>{t('plugins.howtoDesc')}</CardDescription>
        </CardHeader>
        <CardContent>
          <pre className="overflow-x-auto rounded-[var(--radius-sm)] bg-muted/50 p-3 font-mono text-[11px] leading-relaxed">
{`# ~/.antares/plugins/audit/plugin.yaml
name: audit
description: Log every terminal command
command: ./run.sh
hooks: [pre_tool_call]

# ~/.antares/plugins/audit/run.sh
#!/bin/sh
cat >> ~/antares-audit.log
echo '{}'`}
          </pre>
        </CardContent>
      </Card>
    </PageLayout>
  )
}
