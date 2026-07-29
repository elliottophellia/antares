import { useState } from 'react'
import { ChartLineUp } from '@phosphor-icons/react'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
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
import { useI18n, type TFunc } from '@/lib/i18n'

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
              <UsageChart series={data.series} t={t} />
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

// UsageChart draws a smooth stacked area chart (output + input tokens) with
// gradient fills, a themed tooltip, and gridlines, via Recharts. Colours come
// from CSS variables so it tracks the light/dark theme.
function UsageChart({ series, t }: { series: UsagePoint[]; t: TFunc }) {
  return (
    <div>
      <ResponsiveContainer width="100%" height={224}>
        <AreaChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -8 }}>
          <defs>
            <linearGradient id="gradOut" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--primary)" stopOpacity={0.5} />
              <stop offset="100%" stopColor="var(--primary)" stopOpacity={0.04} />
            </linearGradient>
            <linearGradient id="gradIn" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--success)" stopOpacity={0.5} />
              <stop offset="100%" stopColor="var(--success)" stopOpacity={0.04} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" strokeOpacity={0.5} vertical={false} />
          <XAxis
            dataKey="bucket"
            tick={{ fontSize: 10, fill: 'var(--muted-foreground)' }}
            tickLine={false}
            axisLine={{ stroke: 'var(--border)' }}
            minTickGap={24}
          />
          <YAxis
            tick={{ fontSize: 10, fill: 'var(--muted-foreground)' }}
            tickLine={false}
            axisLine={false}
            width={48}
            tickFormatter={(v: number) => formatCount(v)}
          />
          <Tooltip
            content={({ active, payload, label }) => (
              <ChartTooltip
                active={active}
                payload={payload as readonly TooltipEntry[] | undefined}
                label={label as string | number | undefined}
                t={t}
              />
            )}
            cursor={{ stroke: 'var(--border)', strokeWidth: 1 }}
          />
          <Area
            type="monotone"
            dataKey="tokens_out"
            stackId="1"
            stroke="var(--primary)"
            strokeWidth={2}
            fill="url(#gradOut)"
            name={t('analytics.tokensOut')}
          />
          <Area
            type="monotone"
            dataKey="tokens_in"
            stackId="1"
            stroke="var(--success)"
            strokeWidth={2}
            fill="url(#gradIn)"
            name={t('analytics.tokensIn')}
          />
        </AreaChart>
      </ResponsiveContainer>

      <div className="mt-2 flex items-center justify-center gap-4 text-[10px] text-muted-foreground">
        <Legend swatch="var(--primary)" label={t('analytics.tokensOut')} />
        <Legend swatch="var(--success)" label={t('analytics.tokensIn')} />
      </div>
    </div>
  )
}

interface TooltipEntry {
  name?: string
  value?: number
  dataKey?: string | number
  color?: string
}

function ChartTooltip({
  active,
  payload,
  label,
  t,
}: {
  active?: boolean
  payload?: readonly TooltipEntry[]
  label?: string | number
  t: TFunc
}) {
  if (!active || !payload?.length) return null
  const point = (payload[0] as { payload?: UsagePoint })?.payload
  return (
    <div className="rounded-[var(--radius-sm)] border border-border bg-popover px-3 py-2 text-xs shadow-md">
      <p className="font-mono text-[11px] font-medium text-popover-foreground">{label}</p>
      <div className="mt-1 space-y-0.5">
        {payload.map((e) => (
          <Legend
            key={String(e.dataKey)}
            swatch={e.color ?? 'var(--primary)'}
            label={e.name ?? ''}
            value={formatCount(Number(e.value ?? 0))}
          />
        ))}
        {point ? (
          <p className="pt-0.5 text-muted-foreground">
            {point.calls} {t('analytics.calls').toLowerCase()} · ${point.cost.toFixed(4)}
          </p>
        ) : null}
      </div>
    </div>
  )
}

function Legend({ swatch, label, value }: { swatch: string; label: string; value?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="size-2 rounded-[2px]" style={{ background: swatch }} />
      <span className="text-muted-foreground">{label}</span>
      {value != null ? <span className="ml-1 font-medium tabular-nums text-foreground">{value}</span> : null}
    </span>
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
