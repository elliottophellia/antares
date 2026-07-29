import { useEffect, useState } from 'react'
import { ClockCounterClockwise, Play, Plus, Trash } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  EmptyState,
  Input,
  Label,
  Switch,
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
import { SkeletonList } from '@/components/ui/skeleton'

interface CronJob {
  id: string
  name: string
  schedule: string
  prompt: string
  enabled: boolean
  target: string
  last_run: string | null
  next_run: string | null
  last_state: string
}

const EMPTY_DRAFT = { name: '', schedule: '0 8 * * *', prompt: '' }

export default function CronPage() {
  const { t, locale } = useI18n()
  const timeAgo = useTimeAgo()
  const { data, loading, reload } = useApi<{ jobs: CronJob[] }>('/cron/jobs')
  const [busy, setBusy] = useState('')
  const [open, setOpen] = useState(false)

  usePageActions(
    <Button size="sm" onClick={() => setOpen(true)} className="gap-1.5">
      <Plus className="size-4" />
      {t('common.new')}
    </Button>,
    [t],
  )

  const act = async (id: string, fn: () => Promise<unknown>) => {
    setBusy(id)
    try {
      await fn()
      reload()
    } finally {
      setBusy('')
    }
  }

  return (
    <PageLayout>
      <NewJobDialog open={open} onOpenChange={setOpen} onCreated={reload} />

      {loading && !data ? (
        <SkeletonList count={3} />
      ) : (data?.jobs.length ?? 0) === 0 ? (
        <EmptyState
          icon={<ClockCounterClockwise className="size-8" />}
          title={t('cron.none')}
          description={t('cron.noneDesc')}
          action={
            <Button size="sm" onClick={() => setOpen(true)} className="gap-1.5">
              <Plus className="size-4" />
              {t('cron.create')}
            </Button>
          }
        />
      ) : (
        <div className="space-y-2">
          {data!.jobs.map((j) => (
            <Card key={j.id} className="p-3.5">
              <div className="flex items-start gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-medium">{j.name}</span>
                    <Badge variant="outline" className="font-mono">
                      {j.schedule}
                    </Badge>
                    {j.last_state ? (
                      <Badge variant={j.last_state === 'ok' ? 'success' : 'destructive'}>
                        {j.last_state}
                      </Badge>
                    ) : null}
                  </div>
                  <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{j.prompt}</p>
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    {t('cron.lastRun', {
                      time: timeAgo(j.last_run),
                      next: j.next_run ? new Date(j.next_run).toLocaleString(locale) : '—',
                    })}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Switch
                    checked={j.enabled}
                    disabled={busy === j.id}
                    onCheckedChange={(v) =>
                      act(j.id, () => post(`/cron/jobs/${j.id}/toggle`, { enabled: v }))
                    }
                    aria-label={t('common.enable')}
                  />
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={busy === j.id}
                    onClick={() => act(j.id, () => post(`/cron/jobs/${j.id}/run`))}
                    aria-label={t('common.run')}
                  >
                    <Play className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={busy === j.id}
                    onClick={() => act(j.id, () => del(`/cron/jobs/${j.id}`))}
                    aria-label={t('common.delete')}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <Trash className="size-4" />
                  </Button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </PageLayout>
  )
}

function NewJobDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onCreated: () => void
}) {
  const { t, locale } = useI18n()
  const [draft, setDraft] = useState(EMPTY_DRAFT)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [preview, setPreview] = useState<{ valid: boolean; upcoming?: string[]; error?: string }>()

  // Start each visit from a clean slate rather than a half-typed previous job.
  useEffect(() => {
    if (open) {
      setDraft(EMPTY_DRAFT)
      setError(undefined)
    }
  }, [open])

  // Preview the next few activations so a wrong expression is obvious before
  // the job is ever created.
  useEffect(() => {
    const expr = draft.schedule.trim()
    if (!open || !expr) {
      setPreview(undefined)
      return
    }
    const timer = setTimeout(() => {
      get<{ valid: boolean; upcoming?: string[]; error?: string }>(
        `/cron/validate?schedule=${encodeURIComponent(expr)}`,
      )
        .then(setPreview)
        .catch(() => setPreview(undefined))
    }, 350)
    return () => clearTimeout(timer)
  }, [draft.schedule, open])

  const create = async () => {
    if (!draft.name.trim() || !draft.prompt.trim()) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/cron/jobs', draft)
      onCreated()
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const invalid = preview?.valid === false || !draft.name.trim() || !draft.prompt.trim()

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('cron.newSchedule')}</DialogTitle>
          <DialogDescription>{t('cron.newScheduleDesc')}</DialogDescription>
        </DialogHeader>

        <DialogBody>
          <div className="space-y-1.5">
            <Label htmlFor="cron-name">{t('cron.name')}</Label>
            <Input
              id="cron-name"
              autoFocus
              value={draft.name}
              onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
              placeholder={t('cron.namePlaceholder')}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="cron-schedule">{t('cron.schedule')}</Label>
            <Input
              id="cron-schedule"
              value={draft.schedule}
              onChange={(e) => setDraft((d) => ({ ...d, schedule: e.target.value }))}
              className="font-mono"
              aria-invalid={preview?.valid === false}
            />
            {preview ? (
              preview.valid ? (
                <p className="text-[11px] leading-relaxed text-muted-foreground">
                  {t('cron.nextRuns')}:{' '}
                  {(preview.upcoming ?? [])
                    .slice(0, 3)
                    // A full locale timestamp three times over wrapped to two lines on
                    // a phone; day and time is enough to sanity-check a schedule.
                    .map((iso) =>
                      new Date(iso).toLocaleString(locale, {
                        day: 'numeric',
                        month: 'short',
                        hour: '2-digit',
                        minute: '2-digit',
                      }),
                    )
                    .join(' · ')}
                </p>
              ) : (
                <p className="text-[11px] text-destructive">{preview.error}</p>
              )
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="cron-prompt">{t('cron.instruction')}</Label>
            <Textarea
              id="cron-prompt"
              value={draft.prompt}
              onChange={(e) => setDraft((d) => ({ ...d, prompt: e.target.value }))}
              placeholder={t('cron.instructionPlaceholder')}
              className="h-24"
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
          <Button size="sm" onClick={create} loading={saving} disabled={invalid} className="gap-1.5">
            <Plus className="size-4" />
            {t('cron.create')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
