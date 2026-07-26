import { useEffect, useMemo, useState } from 'react'
import {
  CaretDown,
  CheckCircle,
  Eye,
  EyeSlash,
  FloppyDisk,
  MagnifyingGlass,
  Sparkle,
  Warning,
} from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageBody } from '@/components/layout/AppShell'
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
  Input,
  Label,
  Switch,
  Textarea,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'

type Tier = 'essential' | 'common' | 'advanced'

interface Field {
  path: string
  label: string
  group: string
  type: 'string' | 'number' | 'boolean' | 'string[]' | 'object'
  tier: Tier
  default: unknown
  secret: boolean
  enum?: string[]
  help?: string
}

interface ConfigResponse {
  values: Record<string, unknown>
  schema: Field[]
}

const ESSENTIALS = '__essentials'
const YAML = '__yaml'

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
  const [section, setSection] = useState<string>(ESSENTIALS)
  const [showAdvanced, setShowAdvanced] = useState(false)

  const query = filter.trim().toLowerCase()
  const searching = query.length > 0
  const dirty = Object.keys(edits).length

  const fields = useMemo(() => (data?.schema ?? []).filter((f) => f.type !== 'object'), [data])

  const groups = useMemo(() => {
    const seen: string[] = []
    for (const f of fields) if (!seen.includes(f.group)) seen.push(f.group)
    return seen
  }, [fields])

  // Searching spans every group and tier so nothing hides behind disclosure.
  const results = useMemo(
    () =>
      fields.filter(
        (f) =>
          !query || f.path.toLowerCase().includes(query) || f.label.toLowerCase().includes(query),
      ),
    [fields, query],
  )

  const sectionFields = useMemo(() => {
    if (section === ESSENTIALS) return fields.filter((f) => f.tier === 'essential')
    return fields.filter((f) => f.group === section)
  }, [fields, section])

  const visible = sectionFields.filter((f) => showAdvanced || f.tier !== 'advanced')
  const hiddenCount = sectionFields.length - visible.length

  const dirtyPerGroup = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const f of fields) if (f.path in edits) counts[f.group] = (counts[f.group] ?? 0) + 1
    return counts
  }, [fields, edits])

  const dirtyEssentials = useMemo(
    () => fields.filter((f) => f.tier === 'essential' && f.path in edits).length,
    [fields, edits],
  )

  // Reset disclosure when moving between sections.
  useEffect(() => setShowAdvanced(false), [section])

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
    if (!dirty) return
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

  usePageActions(
    <Button size="sm" onClick={save} loading={saving} disabled={!dirty} className="gap-1.5">
      {saved ? <CheckCircle className="size-4" weight="fill" /> : <FloppyDisk className="size-4" />}
      {saved ? t('common.saved') : dirty ? t('config.saveN', { n: dirty }) : t('common.save')}
    </Button>,
    [dirty, saving, saved, t],
  )

  const renderRows = (list: Field[], withGroup = false) => (
    <Card>
      <CardContent className="divide-y divide-border p-0">
        {list.map((f) => (
          <FieldRow
            key={f.path}
            field={f}
            showGroup={withGroup}
            value={valueOf(f)}
            dirty={f.path in edits}
            revealed={!!revealed[f.path]}
            onReveal={() => setRevealed((r) => ({ ...r, [f.path]: !r[f.path] }))}
            onChange={(v) => setValue(f.path, v)}
          />
        ))}
      </CardContent>
    </Card>
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
        <div className="grid gap-5 lg:grid-cols-[13rem_1fr]">
          <Skeleton className="hidden h-80 w-full rounded-[var(--radius-lg)] lg:block" />
          <SkeletonList count={5} />
        </div>
      ) : !data ? (
        <EmptyState title={t('config.loadFailed')} />
      ) : searching ? (
        // Search replaces the layout entirely: one flat list, group shown per row.
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">{t('config.matches', { n: results.length })}</p>
          {results.length === 0 ? (
            <EmptyState title={t('config.noMatch')} />
          ) : (
            renderRows(results, true)
          )}
        </div>
      ) : (
        <div className="grid gap-5 lg:grid-cols-[13rem_1fr] lg:items-start">
          <SectionRail
            groups={groups}
            section={section}
            onSelect={setSection}
            dirtyPerGroup={dirtyPerGroup}
            dirtyEssentials={dirtyEssentials}
          />

          <div className="min-w-0 space-y-4">
            {section === YAML ? (
              <Card>
                <CardHeader>
                  <CardTitle>{t('config.editDirect')}</CardTitle>
                  <CardDescription>{t('config.editDirectDesc')}</CardDescription>
                </CardHeader>
                <CardContent className="space-y-3">
                  {rawState.loading ? (
                    <Skeleton className="h-[55dvh] w-full" />
                  ) : (
                    <Textarea
                      value={yamlDraft ?? rawState.data?.yaml ?? ''}
                      onChange={(e) => setYamlDraft(e.target.value)}
                      spellCheck={false}
                      className="h-[55dvh] font-mono text-xs"
                    />
                  )}
                  <Button size="sm" onClick={saveYaml} loading={saving} disabled={yamlDraft === null}>
                    {t('config.saveYaml')}
                  </Button>
                </CardContent>
              </Card>
            ) : (
              <>
                {section === ESSENTIALS ? (
                  <p className="text-xs leading-relaxed text-muted-foreground sm:text-sm">
                    {t('config.essentialsHint')}
                  </p>
                ) : null}

                {visible.length === 0 ? (
                  <EmptyState title={t('config.nothingHere')} />
                ) : (
                  renderRows(visible)
                )}

                {hiddenCount > 0 ? (
                  <button
                    onClick={() => setShowAdvanced(true)}
                    className="flex w-full items-center justify-center gap-1.5 rounded-[var(--radius-sm)] border border-dashed border-border py-2.5 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground"
                  >
                    <CaretDown className="size-3.5" />
                    {t('config.showAdvanced', { n: hiddenCount })}
                  </button>
                ) : showAdvanced && sectionFields.some((f) => f.tier === 'advanced') ? (
                  <button
                    onClick={() => setShowAdvanced(false)}
                    className="flex w-full items-center justify-center gap-1.5 rounded-[var(--radius-sm)] border border-dashed border-border py-2.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
                  >
                    <CaretDown className="size-3.5 rotate-180" />
                    {t('config.hideAdvanced')}
                  </button>
                ) : null}
              </>
            )}
          </div>
        </div>
      )}
    </PageBody>
  )
}

function SectionRail({
  groups,
  section,
  onSelect,
  dirtyPerGroup,
  dirtyEssentials,
}: {
  groups: string[]
  section: string
  onSelect: (s: string) => void
  dirtyPerGroup: Record<string, number>
  dirtyEssentials: number
}) {
  const { t } = useI18n()

  const dot = (n: number) =>
    n > 0 ? <span className="size-1.5 shrink-0 rounded-full bg-primary" /> : null

  const item = (id: string, label: string, badge?: React.ReactNode, icon?: React.ReactNode) => (
    <button
      key={id}
      onClick={() => onSelect(id)}
      className={cn(
        'flex w-full items-center gap-2 rounded-[var(--radius-sm)] px-3 py-2 text-left text-sm transition-colors',
        section === id
          ? 'bg-primary/12 font-medium text-primary'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      {icon}
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {badge}
    </button>
  )

  return (
    <>
      <nav className="hidden lg:sticky lg:top-4 lg:block">
        <div className="space-y-0.5">
          {item(
            ESSENTIALS,
            t('config.essentials'),
            dot(dirtyEssentials),
            <Sparkle
              className="size-4 shrink-0"
              weight={section === ESSENTIALS ? 'fill' : 'regular'}
            />,
          )}
          <div className="my-1.5 h-px bg-border" />
          {groups.map((g) => item(g, humanizeGroup(g), dot(dirtyPerGroup[g] ?? 0)))}
          <div className="my-1.5 h-px bg-border" />
          {item(YAML, t('config.yamlSection'))}
        </div>
      </nav>

      <div className="lg:hidden">
        <select
          value={section}
          onChange={(e) => onSelect(e.target.value)}
          className="h-10 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
          aria-label={t('config.title')}
        >
          <option value={ESSENTIALS}>{t('config.essentials')}</option>
          {groups.map((g) => (
            <option key={g} value={g}>
              {humanizeGroup(g)}
              {dirtyPerGroup[g] ? ' •' : ''}
            </option>
          ))}
          <option value={YAML}>{t('config.yamlSection')}</option>
        </select>
      </div>
    </>
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

  // Booleans read best as one compact row with the switch on the right.
  if (field.type === 'boolean') {
    return (
      <div className="flex items-center gap-4 px-4 py-3 sm:px-5">
        <div className="min-w-0 flex-1">
          <FieldLabel field={field} dirty={dirty} showGroup={showGroup} />
          {field.help ? (
            <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{field.help}</p>
          ) : null}
        </div>
        <Switch checked={!!value} onCheckedChange={onChange} />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-start sm:gap-4 sm:px-5">
      <div className="min-w-0 sm:w-[42%] sm:pt-1.5">
        <FieldLabel field={field} dirty={dirty} showGroup={showGroup} />
        {field.help ? (
          <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{field.help}</p>
        ) : null}
      </div>

      <div className="min-w-0 sm:flex-1">
        {field.enum ? (
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

function FieldLabel({
  field,
  dirty,
  showGroup,
}: {
  field: Field
  dirty: boolean
  showGroup?: boolean
}) {
  const { t } = useI18n()
  return (
    <>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Label className={cn('text-sm', dirty && 'text-primary')}>{field.label}</Label>
        {showGroup ? <Badge variant="outline">{humanizeGroup(field.group)}</Badge> : null}
        {dirty ? <Badge>{t('config.changed')}</Badge> : null}
      </div>
      <p className="truncate font-mono text-[10px] text-muted-foreground/70">{field.path}</p>
    </>
  )
}
