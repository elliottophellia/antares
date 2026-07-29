import { useEffect, useState } from 'react'
import {
  MagnifyingGlass,
  PencilSimple,
  Plus,
  ShieldCheck,
  Sparkle,
  Storefront,
  TrashSimple,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Pagination } from '@/components/ui/Pagination'
import { Button } from '@/components/ui/button'
import {
  Badge,
  EmptyState,
  Input,
  Label,
  Switch,
  Tabs,
  TabsList,
  TabsTrigger,
  Textarea,
} from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { HubDialog } from '@/components/hub/HubDialog'

interface Skill {
  name: string
  description: string
  path: string
  enabled: boolean
  source: string
  tags?: string[]
  triggers?: string[]
  updated_at: string
  usage_count: number
}

export default function SkillsPage() {
  const { t } = useI18n()
  const [filter, setFilter] = useState('')
  const [query, setQuery] = useState('')
  const endpoint = query ? `/skills?q=${encodeURIComponent(query)}` : '/skills'
  const { data, loading, reload } = useApi<{ skills: Skill[]; library?: number }>(endpoint, [
    endpoint,
  ])
  const [busy, setBusy] = useState('')
  const [browsing, setBrowsing] = useState(false)
  const [editing, setEditing] = useState<Skill | null>(null)
  const [creating, setCreating] = useState(false)
  const [tab, setTab] = useState<'everyday' | 'library'>('everyday')

  usePageActions(
    <>
      <Button size="sm" variant="outline" onClick={() => setBrowsing(true)} className="gap-1.5">
        <Storefront className="size-4" />
        {t('hub.browse')}
      </Button>
      <Button size="sm" onClick={() => setCreating(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('common.new')}
      </Button>
    </>,
    [t],
  )

  useEffect(() => {
    const id = setTimeout(() => setQuery(filter.trim()), 300)
    return () => clearTimeout(id)
  }, [filter])

  const toggle = async (name: string, enabled: boolean) => {
    setBusy(name)
    try {
      await post('/skills/toggle', { name, enabled })
      reload()
    } finally {
      setBusy('')
    }
  }

  const skills = data?.skills ?? []
  const library = data?.library ?? 0

  const header = (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Tabs value={tab} onValueChange={(v) => setTab(v as 'everyday' | 'library')}>
          <TabsList>
            <TabsTrigger value="everyday" className="gap-1.5">
              <Sparkle className="size-3.5" /> {t('skills.tabEveryday')}
            </TabsTrigger>
            {library > 0 ? (
              <TabsTrigger value="library" className="gap-1.5">
                <ShieldCheck className="size-3.5" /> {t('skills.tabLibrary', { n: library })}
              </TabsTrigger>
            ) : null}
          </TabsList>
        </Tabs>
      </div>
      {tab === 'everyday' ? (
        <div className="relative">
          <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t('skills.searchPlaceholder')}
            className="pl-9"
          />
        </div>
      ) : null}
    </div>
  )

  return (
    <PageLayout header={header}>
      <HubDialog kind="skills" open={browsing} onOpenChange={setBrowsing} onInstalled={reload} />
      {(editing || creating) && (
        <SkillEditor
          skill={editing}
          onClose={() => {
            setEditing(null)
            setCreating(false)
          }}
          onSaved={() => {
            setEditing(null)
            setCreating(false)
            reload()
          }}
        />
      )}

      {tab === 'library' && library > 0 ? (
        <SecurityLibrary />
      ) : loading && !data ? (
        <SkeletonList count={5} />
      ) : skills.length === 0 ? (
        <EmptyState
          icon={<Sparkle className="size-8" />}
          title={t('skills.none')}
          description={t('skills.noneDesc')}
          action={
            <div className="flex flex-wrap justify-center gap-2">
              <Button size="sm" onClick={() => setBrowsing(true)} className="gap-1.5">
                <Storefront className="size-4" />
                {t('hub.browse')}
              </Button>
              <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                {t('skills.compose')}
              </Button>
            </div>
          }
        />
      ) : (
        <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
          {skills.map((s) => (
            <div
              key={s.name}
              className={cn(
                'group flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-3.5 transition-colors hover:border-primary/40',
                !s.enabled && 'opacity-60',
              )}
            >
              <button onClick={() => setEditing(s)} className="min-w-0 flex-1 text-left">
                <div className="flex items-start justify-between gap-2">
                  <span className="min-w-0 truncate text-sm font-medium">{s.name}</span>
                  <PencilSimple className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
                </div>
                <p className="mt-1.5 line-clamp-2 text-xs text-muted-foreground">{s.description}</p>
                <div className="mt-2 flex flex-wrap gap-1.5">
                  <Badge variant="outline">{s.source}</Badge>
                  {s.usage_count > 0 ? (
                    <Badge variant="secondary">{t('skills.used', { n: s.usage_count })}</Badge>
                  ) : null}
                </div>
              </button>
              <div className="mt-3 flex items-center justify-between border-t border-border pt-2.5">
                <label className="flex items-center gap-2 text-[11px] text-muted-foreground">
                  <Switch
                    checked={s.enabled}
                    disabled={busy === s.name}
                    onCheckedChange={(v) => toggle(s.name, v)}
                    aria-label={`${t('common.enable')} ${s.name}`}
                  />
                  {s.enabled ? t('skills.on') : t('skills.off')}
                </label>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  disabled={busy === s.name}
                  onClick={() => del(`/skills/${encodeURIComponent(s.name)}`).then(reload)}
                  aria-label={t('common.delete')}
                  className="text-muted-foreground hover:text-destructive"
                >
                  <TrashSimple className="size-4" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </PageLayout>
  )
}

// ---- everyday skill editor (create + edit) ----------------------------------

function SkillEditor({
  skill,
  onClose,
  onSaved,
}: {
  skill: Skill | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const isNew = !skill
  const [draft, setDraft] = useState({
    name: skill?.name ?? '',
    description: skill?.description ?? '',
    body: '',
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()

  // Load the body when editing (the list omits it).
  useEffect(() => {
    if (!skill) return
    let cancelled = false
    get<{ body: string }>(`/skills/${encodeURIComponent(skill.name)}`)
      .then((r) => {
        if (!cancelled) setDraft((d) => ({ ...d, body: r.body }))
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [skill])

  const save = async () => {
    if (!draft.name.trim() || !draft.body.trim()) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/skills', draft)
      onSaved()
    } catch (e) {
      setError((e as Error).message)
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{isNew ? t('skills.compose') : skill!.name}</DialogTitle>
        </DialogHeader>

        <DialogBody className="space-y-3.5">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="skill-name">{t('skills.name')}</Label>
              <Input
                id="skill-name"
                autoFocus={isNew}
                disabled={!isNew}
                value={draft.name}
                onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
                placeholder="deploy-homeserver"
              />
              {!isNew ? <p className="text-[11px] text-muted-foreground">{t('skills.nameLocked')}</p> : null}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="skill-desc">{t('skills.whenToUse')}</Label>
              <Input
                id="skill-desc"
                value={draft.description}
                onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
                placeholder={t('skills.whenToUsePlaceholder')}
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="skill-body">{t('skills.procedure')}</Label>
            <Textarea
              id="skill-body"
              value={draft.body}
              onChange={(e) => setDraft((d) => ({ ...d, body: e.target.value }))}
              placeholder={t('skills.procedurePlaceholder')}
              className="h-64 font-mono text-xs leading-relaxed"
            />
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>

        <DialogFooter className="flex items-center">
          {!isNew ? (
            <Button
              variant="ghost"
              size="sm"
              disabled={saving}
              onClick={() => del(`/skills/${encodeURIComponent(skill!.name)}`).then(onSaved)}
              className="mr-auto gap-1.5 text-destructive hover:text-destructive"
            >
              <TrashSimple className="size-4" />
              {t('common.delete')}
            </Button>
          ) : null}
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button
            size="sm"
            onClick={save}
            loading={saving}
            disabled={!draft.name.trim() || !draft.body.trim()}
          >
            {t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- security library (read-only, browse by category) -----------------------

interface LibSkill {
  name: string
  description: string
  category?: string
}

const LIB_LIMIT = 40

/**
 * The bundled security library is thousands of skills. Browse them by category,
 * a page at a time, reading each in place. Read-only; loads only when opened so
 * the everyday tab stays fast.
 */
function SecurityLibrary() {
  const { t } = useI18n()
  const [category, setCategory] = useState('')
  const [offset, setOffset] = useState(0)
  const [reading, setReading] = useState<string | null>(null)
  const [body, setBody] = useState('')

  const { data, loading } = useApi<{
    skills: LibSkill[]
    total: number
    categories: Record<string, number>
  }>(`/skills/library?category=${encodeURIComponent(category)}&offset=${offset}&limit=${LIB_LIMIT}`, [
    category,
    offset,
  ])

  const read = async (name: string) => {
    if (reading === name) {
      setReading(null)
      return
    }
    setReading(name)
    setBody('')
    try {
      const r = await get<{ body: string }>(`/skills/${encodeURIComponent(name)}`)
      setBody(r.body)
    } catch (e) {
      setBody((e as Error).message)
    }
  }

  const categories = Object.entries(data?.categories ?? {}).sort((a, b) => b[1] - a[1])
  const total = data?.total ?? 0

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-1.5">
        <CategoryChip active={category === ''} onClick={() => { setCategory(''); setOffset(0) }}>
          {t('skills.allCategories')}
        </CategoryChip>
        {categories.map(([cat, n]) => (
          <CategoryChip
            key={cat}
            active={category === cat}
            onClick={() => { setCategory(cat); setOffset(0) }}
          >
            {cat} <span className="opacity-60">{n}</span>
          </CategoryChip>
        ))}
      </div>

      {loading && !data ? (
        <SkeletonList count={6} />
      ) : (
        <div className="space-y-1.5">
          {(data?.skills ?? []).map((s) => (
            <div key={s.name} className="rounded-[var(--radius-sm)] border border-border">
              <button onClick={() => read(s.name)} className="w-full p-2.5 text-left">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 truncate font-mono text-xs font-medium">{s.name}</span>
                  {s.category ? <Badge variant="outline">{s.category}</Badge> : null}
                </div>
                <p className="mt-0.5 text-[11px] text-muted-foreground">{s.description}</p>
              </button>
              {reading === s.name ? (
                <div className="border-t border-border p-2.5">
                  {body === '' ? (
                    <Skeleton className="h-24 w-full" />
                  ) : (
                    <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-[10px] leading-relaxed">
                      {body}
                    </pre>
                  )}
                </div>
              ) : null}
            </div>
          ))}
        </div>
      )}

      {total > LIB_LIMIT ? (
        <Pagination offset={offset} limit={LIB_LIMIT} total={total} onChange={setOffset} />
      ) : null}
    </div>
  )
}

function CategoryChip({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'rounded-full border px-2.5 py-1 text-[11px] transition-colors',
        active
          ? 'border-primary bg-primary/10 text-primary'
          : 'border-border text-muted-foreground hover:border-primary/40',
      )}
    >
      {children}
    </button>
  )
}
