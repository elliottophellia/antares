import { useEffect, useMemo, useState } from 'react'
import {
  CheckCircle,
  Eye,
  EyeSlash,
  FloppyDisk,
  MagnifyingGlass,
  Warning,
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
  Switch,
  Textarea,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { usePageActions } from '@/components/layout/PageChrome'

interface Field {
  path: string
  label: string
  group: string
  type: 'string' | 'number' | 'boolean' | 'string[]' | 'object'
  default: unknown
  secret: boolean
  enum?: string[]
  help?: string
}

interface ConfigResponse {
  values: Record<string, unknown>
  schema: Field[]
}

const YAML_GROUP = '__yaml'

/** Read a dotted path out of the nested config object. */
function readPath(obj: Record<string, unknown>, path: string): unknown {
  return path.split('.').reduce<unknown>((acc, key) => {
    if (acc && typeof acc === 'object') return (acc as Record<string, unknown>)[key]
    return undefined
  }, obj)
}

/** "prompt_caching" → "Prompt caching" */
function humanizeGroup(name: string): string {
  const spaced = name.replace(/_/g, ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

export default function ConfigPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ConfigResponse>('/config')
  const rawState = useApi<{ yaml: string }>('/config/raw')

  const [edits, setEdits] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string>()
  const [filter, setFilter] = useState('')
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})
  const [yamlDraft, setYamlDraft] = useState<string | null>(null)
  const [group, setGroup] = useState<string>('')

  const groups = useMemo(() => {
    if (!data) return []
    const seen: string[] = []
    for (const f of data.schema) if (!seen.includes(f.group)) seen.push(f.group)
    return seen
  }, [data])

  useEffect(() => {
    if (!group && groups.length) setGroup(groups[0])
  }, [groups, group])

  const query = filter.trim().toLowerCase()
  const searching = query.length > 0

  const matches = (f: Field) =>
    f.type !== 'object' &&
    (!query || f.path.toLowerCase().includes(query) || f.label.toLowerCase().includes(query))

  // While searching, results span every group so nothing hides behind a tab.
  const visibleFields = useMemo(() => {
    const all = (data?.schema ?? []).filter(matches)
    return searching ? all : all.filter((f) => f.group === group)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, group, query])

  const countPerGroup = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const f of data?.schema ?? []) {
      if (f.type === 'object') continue
      counts[f.group] = (counts[f.group] ?? 0) + 1
    }
    return counts
  }, [data])

  const dirtyPerGroup = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const f of data?.schema ?? []) {
      if (f.path in edits) counts[f.group] = (counts[f.group] ?? 0) + 1
    }
    return counts
  }, [data, edits])

  const valueOf = (f: Field): unknown => {
    if (f.path in edits) return edits[f.path]
    const v = data ? readPath(data.values, f.path) : undefined
    return v === undefined ? f.default : v
  }

  const setValue = (path: string, v: unknown) => {
    setEdits((prev) => ({ ...prev, [path]: v }))
    setSaved(false)
  }

  const save = async () => {
    if (Object.keys(edits).length === 0) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/config', { updates: edits })
      setEdits({})
      setSaved(true)
      reload()
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const saveYaml = async () => {
    if (yamlDraft === null) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/config/raw', { yaml: yamlDraft })
      setYamlDraft(null)
      reload()
      rawState.reload()
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const dirty = Object.keys(edits).length

  usePageActions(
    <Button size="sm" onClick={save} loading={saving} disabled={!dirty} className="gap-1.5">
      {saved ? <CheckCircle className="size-4" weight="fill" /> : <FloppyDisk className="size-4" />}
      {saved ? t('common.saved') : dirty ? t('config.saveN', { n: dirty }) : t('common.save')}
    </Button>,
    [dirty, saving, saved, t],
  )

  return (
    <PageBody>
      {error ? (
        <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
          <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
          <span className="min-w-0 break-words">{error}</span>
        </div>
      ) : null}

      <div className="relative">
        <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={t('config.searchPlaceholder')}
          className="pl-9"
        />
      </div>

      {loading && !data ? (
        <div className="grid gap-4 lg:grid-cols-[13rem_1fr]">
          <Skeleton className="hidden h-96 w-full rounded-[var(--radius-lg)] lg:block" />
          <SkeletonList count={6} />
        </div>
      ) : !data ? (
        <EmptyState title={t('config.loadFailed')} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-[13rem_1fr] lg:items-start">
          {/* Group picker: a vertical rail on desktop, a select on mobile —
              far easier to reach than a long horizontal tab strip. */}
          <nav className="hidden lg:sticky lg:top-4 lg:block">
            <ul className="space-y-0.5">
              {groups.map((g) => (
                <li key={g}>
                  <button
                    onClick={() => {
                      setGroup(g)
                      setFilter('')
                    }}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-[var(--radius-sm)] px-3 py-2 text-left text-sm transition-colors',
                      !searching && g === group
                        ? 'bg-primary/12 font-medium text-primary'
                        : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                    )}
                  >
                    <span className="min-w-0 flex-1 truncate">{humanizeGroup(g)}</span>
                    {dirtyPerGroup[g] ? (
                      <span className="size-1.5 shrink-0 rounded-full bg-primary" />
                    ) : (
                      <span className="shrink-0 text-[10px] tabular-nums opacity-60">
                        {countPerGroup[g] ?? 0}
                      </span>
                    )}
                  </button>
                </li>
              ))}
              <li className="pt-1">
                <button
                  onClick={() => {
                    setGroup(YAML_GROUP)
                    setFilter('')
                  }}
                  className={cn(
                    'w-full rounded-[var(--radius-sm)] px-3 py-2 text-left text-sm transition-colors',
                    !searching && group === YAML_GROUP
                      ? 'bg-primary/12 font-medium text-primary'
                      : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                  )}
                >
                  YAML
                </button>
              </li>
            </ul>
          </nav>

          <div className="lg:hidden">
            <select
              value={searching ? '' : group}
              onChange={(e) => {
                setGroup(e.target.value)
                setFilter('')
              }}
              className="h-10 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
              aria-label={t('config.title')}
            >
              {groups.map((g) => (
                <option key={g} value={g}>
                  {humanizeGroup(g)} ({countPerGroup[g] ?? 0})
                  {dirtyPerGroup[g] ? ' •' : ''}
                </option>
              ))}
              <option value={YAML_GROUP}>YAML</option>
            </select>
          </div>

          <div className="min-w-0 space-y-3">
            {!searching && group === YAML_GROUP ? (
              <Card>
                <CardHeader>
                  <CardTitle>{t('config.editDirect')}</CardTitle>
                  <CardDescription>{t('config.editDirectDesc')}</CardDescription>
                </CardHeader>
                <CardContent className="space-y-3">
                  {rawState.loading ? (
                    <Skeleton className="h-80 w-full" />
                  ) : (
                    <Textarea
                      value={yamlDraft ?? rawState.data?.yaml ?? ''}
                      onChange={(e) => setYamlDraft(e.target.value)}
                      spellCheck={false}
                      className="h-[60dvh] font-mono text-xs"
                    />
                  )}
                  <Button size="sm" onClick={saveYaml} loading={saving} disabled={yamlDraft === null}>
                    {t('config.saveYaml')}
                  </Button>
                </CardContent>
              </Card>
            ) : visibleFields.length === 0 ? (
              <EmptyState title={t('config.noMatch')} />
            ) : (
              <Card>
                <CardContent className="divide-y divide-border pt-5">
                  {visibleFields.map((f) => (
                    <FieldRow
                      key={f.path}
                      field={f}
                      showGroup={searching}
                      value={valueOf(f)}
                      dirty={f.path in edits}
                      revealed={!!revealed[f.path]}
                      onReveal={() => setRevealed((r) => ({ ...r, [f.path]: !r[f.path] }))}
                      onChange={(v) => setValue(f.path, v)}
                    />
                  ))}
                </CardContent>
              </Card>
            )}
          </div>
        </div>
      )}
    </PageBody>
  )
}

function FieldRow({
  field,
  value,
  dirty,
  revealed,
  showGroup,
  onReveal,
  onChange,
}: {
  field: Field
  value: unknown
  dirty: boolean
  revealed: boolean
  showGroup?: boolean
  onReveal: () => void
  onChange: (v: unknown) => void
}) {
  const { t } = useI18n()
  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:gap-4">
      <div className="min-w-0 sm:w-1/2">
        <div className="flex items-center gap-2">
          <Label className={cn(dirty && 'text-primary')}>{field.label}</Label>
          {showGroup ? <Badge variant="outline">{humanizeGroup(field.group)}</Badge> : null}
          {dirty ? <Badge>{t('config.changed')}</Badge> : null}
        </div>
        <p className="truncate font-mono text-[10px] text-muted-foreground">{field.path}</p>
        {field.help ? <p className="mt-0.5 text-[11px] text-muted-foreground">{field.help}</p> : null}
      </div>

      <div className="sm:flex-1">
        {field.type === 'boolean' ? (
          <Switch checked={!!value} onCheckedChange={onChange} />
        ) : field.enum ? (
          <select
            value={String(value ?? '')}
            onChange={(e) => onChange(e.target.value)}
            className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
          >
            {field.enum.map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        ) : field.type === 'string[]' ? (
          <Input
            value={Array.isArray(value) ? (value as string[]).join(', ') : ''}
            placeholder={t('config.commaSeparated')}
            onChange={(e) =>
              onChange(
                e.target.value
                  .split(',')
                  .map((s) => s.trim())
                  .filter(Boolean),
              )
            }
          />
        ) : field.secret ? (
          <div className="flex gap-2">
            <Input
              type={revealed ? 'text' : 'password'}
              value={String(value ?? '')}
              onChange={(e) => onChange(e.target.value)}
              placeholder={t('common.notSet')}
              autoComplete="off"
            />
            <Button variant="outline" size="icon" onClick={onReveal} aria-label={t('config.reveal')}>
              {revealed ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
            </Button>
          </div>
        ) : (
          <Input
            type={field.type === 'number' ? 'number' : 'text'}
            value={String(value ?? '')}
            step="any"
            onChange={(e) =>
              onChange(field.type === 'number' ? Number(e.target.value) : e.target.value)
            }
          />
        )}
      </div>
    </div>
  )
}
