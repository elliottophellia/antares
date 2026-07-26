import { useState } from 'react'
import { CaretDown, Plus, Sparkle, Trash } from '@phosphor-icons/react'
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
  const { data, loading, reload } = useApi<{ skills: Skill[] }>('/skills')
  const [filter, setFilter] = useState('')
  const [busy, setBusy] = useState('')
  const [composing, setComposing] = useState(false)
  const [draft, setDraft] = useState({ name: '', description: '', body: '' })
  const [error, setError] = useState<string>()
  const [expanded, setExpanded] = useState<string | null>(null)
  const [bodies, setBodies] = useState<Record<string, string>>({})

  usePageActions(
    <Button size="sm" onClick={() => setComposing((v) => !v)} className="gap-1.5">
      <Plus className="size-4" />
      {t('common.new')}
    </Button>,
    [composing, t],
  )

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

  const create = async () => {
    if (!draft.name.trim() || !draft.body.trim()) return
    setBusy('new')
    setError(undefined)
    try {
      await post('/skills', draft)
      setDraft({ name: '', description: '', body: '' })
      setComposing(false)
      reload()
    } catch (e) {
      setError((e as Error).message)
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

  const q = filter.trim().toLowerCase()
  const skills = (data?.skills ?? []).filter(
    (s) => !q || s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q),
  )

  return (
    <PageBody>
      {composing ? (
        <Card>
          <CardHeader>
            <CardTitle>{t('skills.compose')}</CardTitle>
            <CardDescription>{t('skills.composeDesc')}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="skill-name">{t('skills.name')}</Label>
                <Input
                  id="skill-name"
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
                className="h-48 font-mono text-xs"
              />
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
            <div className="flex gap-2">
              <Button size="sm" onClick={create} loading={busy === 'new'}>
                {t('common.save')}
              </Button>
              <Button size="sm" variant="outline" onClick={() => setComposing(false)}>
                {t('common.close')}
              </Button>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <Input
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder={t('skills.searchPlaceholder')}
      />

      {loading && !data ? (
        <SkeletonList count={5} />
      ) : skills.length === 0 ? (
        <EmptyState
          icon={<Sparkle className="size-8" />}
          title={t('skills.none')}
          description={t('skills.noneDesc')}
          action={
            <Button size="sm" onClick={() => setComposing(true)}>
              {t('skills.compose')}
            </Button>
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
    </PageBody>
  )
}
