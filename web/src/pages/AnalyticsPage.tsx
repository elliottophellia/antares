import { useMemo, useState } from 'react'
import { ChartLineUp } from '@phosphor-icons/react'
import { useApi } from '@/lib/hooks'
import { formatCount } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonStats } from '@/components/ui/skeleton'
import { useI18n } from '@/lib/i18n'

interface UsagePoint {
  bucket: string
  tokens_in: number
  tokens_out: number
  cost: number
  calls: number
}

interface ModelUsage {
  model: string
  provider: string
  calls: number
  tokens_in: number
  tokens_out: number
  cost: number
}

interface AnalyticsResponse {
  series: UsagePoint[]
  by_model: ModelUsage[]
  totals: { tokens_in: number; tokens_out: number; cost: number; calls: number }
}

const RANGES = [
  { id: '24h', labelKey: 'analytics.range24h', bucket: 'hour' },
  { id: '7d', labelKey: 'analytics.range7d', bucket: 'day' },
  { id: '30d', labelKey: 'analytics.range30d', bucket: 'day' },
] as const

export default function AnalyticsPage() {
  const { t } = useI18n()
  const [range, setRange] = useState<(typeof RANGES)[number]['id']>('7d')
  const bucket = RANGES.find((r) => r.id === range)!.bucket
  const { data, loading } = useApi<AnalyticsResponse>(`/analytics?range=${range}&bucket=${bucket}`, [range])

  const max = useMemo(
    () => Math.max(1, ...(data?.series ?? []).map((p) => p.tokens_in + p.tokens_out)),
    [data],
  )

  return (
    <PageLayout>
      <Tabs value={range} onValueChange={(v) => setRange(v as typeof range)}>
        <TabsList>
          {RANGES.map((r) => (
            <TabsTrigger key={r.id} value={r.id}>
              {t(r.labelKey)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {loading && !data ? (
        <>
          <SkeletonStats count={4} />
          <Skeleton className="h-56 w-full rounded-[var(--radius-lg)]" />
        </>
      ) : !data || data.totals.calls === 0 ? (
        <EmptyState
          icon={<ChartLineUp className="size-8" />}
          title={t('analytics.none')}
          description={t('analytics.noneDesc')}
        />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Stat label={t('analytics.calls')} value={formatCount(data.totals.calls)} />
            <Stat label={t('analytics.tokensIn')} value={formatCount(data.totals.tokens_in)} />
            <Stat label={t('analytics.tokensOut')} value={formatCount(data.totals.tokens_out)} />
            <Stat label={t('analytics.cost')} value={`$${data.totals.cost.toFixed(4)}`} />
          </div>

          <Card>
            <CardHeader>
              <CardTitle>{t('analytics.perPeriod')}</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="flex h-40 items-end gap-1 overflow-x-auto">
                {data.series.map((p) => {
                  const total = p.tokens_in + p.tokens_out
                  const height = Math.max(2, (total / max) * 100)
                  return (
                    <div
                      key={p.bucket}
                      className="group relative flex min-w-3 flex-1 flex-col justify-end"
                      title={`${p.bucket}: ${formatCount(total)} token · $${p.cost.toFixed(4)}`}
                    >
                      <div
                        className="w-full rounded-t-sm bg-primary/70 transition-colors group-hover:bg-primary"
                        style={{ height: `${height}%` }}
                      />
                    </div>
                  )
                })}
              </div>
              <div className="mt-2 flex justify-between text-[10px] text-muted-foreground">
                <span>{data.series[0]?.bucket}</span>
                <span>{data.series[data.series.length - 1]?.bucket}</span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('analytics.byModel')}</CardTitle>
            </CardHeader>
            <CardContent className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead className="text-muted-foreground">
                  <tr className="border-b border-border">
                    <th className="py-2 text-left font-medium">{t('analytics.model')}</th>
                    <th className="py-2 text-right font-medium">{t('analytics.calls')}</th>
                    <th className="py-2 text-right font-medium">{t('analytics.tokensIn')}</th>
                    <th className="py-2 text-right font-medium">{t('analytics.tokensOut')}</th>
                    <th className="py-2 text-right font-medium">{t('analytics.cost')}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.by_model.map((m) => (
                    <tr key={`${m.provider}/${m.model}`} className="border-b border-border last:border-0">
                      <td className="max-w-40 truncate py-2 font-mono">{m.model}</td>
                      <td className="py-2 text-right tabular-nums">{m.calls}</td>
                      <td className="py-2 text-right tabular-nums">{formatCount(m.tokens_in)}</td>
                      <td className="py-2 text-right tabular-nums">{formatCount(m.tokens_out)}</td>
                      <td className="py-2 text-right tabular-nums">${m.cost.toFixed(4)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </CardContent>
          </Card>
        </>
      )}
    </PageLayout>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card className="p-4">
      <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{label}</p>
      <p className="mt-2 text-xl font-semibold tabular-nums">{value}</p>
    </Card>
  )
}
