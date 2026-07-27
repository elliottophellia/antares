import { useEffect, useState } from 'react'
import { CaretDown, Plus, ShieldCheck, Sparkle, Storefront, Trash } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageBody } from '@/components/layout/AppShell'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  EmptyState,
  Input,
  Label,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
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
  const timeAgo = useTimeAgo()
  const [filter, setFilter] = useState('')
  const [query, setQuery] = useState('')
  const [cwe, setCwe] = useState('')
  const [tech, setTech] = useState('')
  const params = new URLSearchParams()
  if (query) params.set('q', query)
  if (cwe.trim()) params.set('cwe', cwe.trim())
  if (tech.trim()) params.set('tech', tech.trim())
  const endpoint = params.toString() ? `/skills?${params.toString()}` : '/skills'
  const { data, loading, reload } = useApi<{ skills: Skill[]; library?: number; searching?: boolean }>(
    endpoint,
    [endpoint],
  )
  const [busy, setBusy] = useState('')
  const [composing, setComposing] = useState(false)
  const [browsing, setBrowsing] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [bodies, setBodies] = useState<Record<string, string>>({})

  usePageActions(
    <>
      <Button size="sm" variant="outline" onClick={() => setBrowsing(true)} className="gap-1.5">
        <Storefront className="size-4" />
        {t('hub.browse')}
      </Button>
      <Button size="sm" onClick={() => setComposing(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('common.new')}
      </Button>
    </>,
    [t],
  )

  useEffect(() => {
    const t = setTimeout(() => setQuery(filter.trim()), 300)
    return () => clearTimeout(t)
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

  const remove = async (name: string) => {
    setBusy(name)
    try {
      await del(`/skills/${encodeURIComponent(name)}`)
      reload()
    } finally {
      setBusy('')
    }
  }

  const openSkill = async (name: string) => {
    if (expanded === name) {
      setExpanded(null)
      return
    }
    setExpanded(name)
    if (bodies[name] !== undefined) return
    try {
      const r = await get<{ body: string }>(`/skills/${encodeURIComponent(name)}`)
      setBodies((b) => ({ ...b, [name]: r.body }))
    } catch (e) {
      setBodies((b) => ({ ...b, [name]: (e as Error).message }))
    }
  }

  const skills = data?.skills ?? []
  const library = data?.library ?? 0

  return (
    <PageBody>
      <NewSkillDialog open={composing} onOpenChange={setComposing} onSaved={reload} />
      <HubDialog kind="skills" open={browsing} onOpenChange={setBrowsing} onInstalled={reload} />

      <Tabs defaultValue="everyday">
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

        <TabsContent value="everyday" className="space-y-4">
      <div className="space-y-1.5">
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={library > 0 ? t('skills.searchLibrary', { n: library }) : t('skills.searchPlaceholder')}
        />
        {library > 0 ? (
          <div className="flex gap-2">
            <Input
              value={cwe}
              onChange={(e) => setCwe(e.target.value)}
              placeholder={t('skills.filterCwe')}
              className="h-8 text-xs"
            />
            <Input
              value={tech}
              onChange={(e) => setTech(e.target.value)}
              placeholder={t('skills.filterTech')}
              className="h-8 text-xs"
            />
          </div>
        ) : null}
        {library > 0 && !query && !cwe && !tech ? (
          <p className="text-[11px] text-muted-foreground">{t('skills.libraryHint', { n: library })}</p>
        ) : null}
        {data?.searching ? (
          <p className="text-[11px] text-muted-foreground">{t('skills.searchingLibrary')}</p>
        ) : null}
      </div>

      {loading && !data ? (
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
              <Button size="sm" variant="outline" onClick={() => setComposing(true)}>
                {t('skills.compose')}
              </Button>
            </div>
          }
        />
      ) : (
        <div className="space-y-2">
          {skills.map((s) => (
            <Card key={s.name}>
              <div className="flex items-start gap-3 p-3.5">
                <button onClick={() => openSkill(s.name)} className="min-w-0 flex-1 text-left">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-medium">{s.name}</span>
                    <Badge variant="outline">{s.source}</Badge>
                    {s.usage_count > 0 ? (
                      <Badge variant="secondary">{t('skills.used', { n: s.usage_count })}</Badge>
                    ) : null}
                    <CaretDown
                      className={cn(
                        'size-3 text-muted-foreground transition-transform',
                        expanded === s.name && 'rotate-180',
                      )}
                    />
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">{s.description}</p>
                  <p className="mt-1 break-all font-mono text-[10px] text-muted-foreground">
                    {s.path} · {timeAgo(s.updated_at)}
                  </p>
                </button>
                <div className="flex shrink-0 items-center gap-1">
                  <Switch
                    checked={s.enabled}
                    disabled={busy === s.name}
                    onCheckedChange={(v) => toggle(s.name, v)}
                    aria-label={`${t('common.enable')} ${s.name}`}
                  />
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={busy === s.name}
                    onClick={() => remove(s.name)}
                    aria-label={t('common.delete')}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <Trash className="size-4" />
                  </Button>
                </div>
              </div>

              {expanded === s.name ? (
                <div className="border-t border-border p-3.5">
                  {bodies[s.name] === undefined ? (
                    <Skeleton className="h-24 w-full" />
                  ) : (
                    <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed">
                      {bodies[s.name]}
                    </pre>
                  )}
                </div>
              ) : null}
            </Card>
          ))}
        </div>
      )}

        </TabsContent>

        {library > 0 ? (
          <TabsContent value="library">
            <SecurityLibrary total={library} />
          </TabsContent>
        ) : null}
      </Tabs>
    </PageBody>
  )
}

interface LibSkill {
  name: string
  description: string
  category?: string
}

/**
 * The bundled security library is thousands of skills. This browses them by
 * category, a page at a time, so they are explorable without knowing exactly
 * what to search for. The list only loads when opened — the everyday page must
 * stay fast.
 */
function SecurityLibrary({ total }: { total: number }) {
  const { t } = useI18n()
  const [open] = useState(true)
  const [category, setCategory] = useState('')
  const [offset, setOffset] = useState(0)
  const [reading, setReading] = useState<string | null>(null)
  const [body, setBody] = useState('')
  const LIMIT = 40

  const { data, loading } = useApi<{
    skills: LibSkill[]
    total: number
    categories: Record<string, number>
  }>(
    open ? `/skills/library?category=${encodeURIComponent(category)}&offset=${offset}&limit=${LIMIT}` : null,
    [open, category, offset],
  )

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
  const shownTotal = data?.total ?? total

  return (
    <Card>
      {open ? (
        <div className="p-3.5">
          <div className="mb-3 flex flex-wrap gap-1.5">
            <button
              onClick={() => {
                setCategory('')
                setOffset(0)
              }}
              className={cn(
                'rounded-full border px-2.5 py-1 text-[11px] transition-colors',
                category === '' ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:border-primary/40',
              )}
            >
              {t('skills.allCategories')}
            </button>
            {categories.map(([cat, n]) => (
              <button
                key={cat}
                onClick={() => {
                  setCategory(cat)
                  setOffset(0)
                }}
                className={cn(
                  'rounded-full border px-2.5 py-1 text-[11px] transition-colors',
                  category === cat ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:border-primary/40',
                )}
              >
                {cat} <span className="opacity-60">{n}</span>
              </button>
            ))}
          </div>

          {loading && !data ? (
            <SkeletonList count={5} />
          ) : (
            <div className="space-y-1.5">
              {(data?.skills ?? []).map((s) => (
                <div key={s.name} className="rounded-[var(--radius-sm)] border border-border">
                  <button onClick={() => read(s.name)} className="w-full p-2.5 text-left">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-xs font-medium">{s.name}</span>
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

          <div className="mt-3 flex items-center justify-between text-xs text-muted-foreground">
            <span>
              {t('skills.showingRange', {
                from: shownTotal === 0 ? 0 : offset + 1,
                to: Math.min(offset + LIMIT, shownTotal),
                total: shownTotal,
              })}
            </span>
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - LIMIT))}
              >
                {t('common.previous')}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={offset + LIMIT >= shownTotal}
                onClick={() => setOffset(offset + LIMIT)}
              >
                {t('common.next')}
              </Button>
            </div>
          </div>
        </div>
      ) : null}
    </Card>
  )
}

function NewSkillDialog({
  open,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [draft, setDraft] = useState({ name: '', description: '', body: '' })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()

  useEffect(() => {
    if (open) {
      setDraft({ name: '', description: '', body: '' })
      setError(undefined)
    }
  }, [open])

  const create = async () => {
    if (!draft.name.trim() || !draft.body.trim()) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/skills', draft)
      onSaved()
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('skills.compose')}</DialogTitle>
          <DialogDescription>{t('skills.composeDesc')}</DialogDescription>
        </DialogHeader>

        <DialogBody>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="skill-name">{t('skills.name')}</Label>
              <Input
                id="skill-name"
                autoFocus
                value={draft.name}
                onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
                placeholder="deploy-homeserver"
              />
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
              className="h-56 font-mono text-xs"
            />
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button
            size="sm"
            onClick={create}
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
