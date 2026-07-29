import { useEffect, useRef, useState } from 'react'
import { Play, Plus, Robot, Trash } from '@phosphor-icons/react'
import { del, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
import { Badge, Card, EmptyState, Input, Label, Textarea } from '@/components/ui/primitives'
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

interface Card_ {
  id: string
  title: string
  prompt: string
  status: 'pending' | 'running' | 'verified' | 'failed' | 'merged'
  result?: string
  verify_output?: string
  branch?: string
  pr?: string
  error?: string
  created_at: string
  updated_at: string
}

const STATUS_VARIANT: Record<Card_['status'], 'outline' | 'warning' | 'success' | 'destructive' | 'secondary'> = {
  pending: 'outline',
  running: 'warning',
  verified: 'success',
  merged: 'success',
  failed: 'destructive',
}

export default function AutopilotPage() {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const { data, loading, reload } = useApi<{ cards: Card_[] }>('/autopilot')
  const [adding, setAdding] = useState(false)
  const [running, setRunning] = useState(false)
  const cards = data?.cards ?? []
  const pending = cards.filter((c) => c.status === 'pending').length
  const active = cards.some((c) => c.status === 'running')

  // Poll while anything is running so status updates land without a refresh.
  const reloadRef = useRef(reload)
  reloadRef.current = reload
  useEffect(() => {
    if (!active) return
    const id = setInterval(() => reloadRef.current(), 2500)
    return () => clearInterval(id)
  }, [active])

  const run = async () => {
    setRunning(true)
    try {
      await post('/autopilot/run', {})
      reload()
    } finally {
      setRunning(false)
    }
  }

  usePageActions(
    <div className="flex items-center gap-2">
      <Button size="sm" variant="outline" onClick={run} loading={running} disabled={pending === 0} className="gap-1.5">
        <Play className="size-4" />
        {t('autopilot.run')} {pending > 0 ? `(${pending})` : ''}
      </Button>
      <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('autopilot.add')}
      </Button>
    </div>,
  )

  return (
    <PageLayout>
      <AddDialog open={adding} onOpenChange={setAdding} onAdded={reload} />

      {loading && !data ? (
        <SkeletonList count={3} />
      ) : cards.length === 0 ? (
        <EmptyState
          icon={<Robot className="size-8" />}
          title={t('autopilot.empty')}
          description={t('autopilot.emptyDesc')}
        />
      ) : (
        <div className="space-y-2.5">
          {cards.map((c) => (
            <Card key={c.id} className="p-3.5">
              <div className="flex items-start gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium">{c.title}</span>
                    <Badge variant={STATUS_VARIANT[c.status]}>{t(`autopilot.status.${c.status}` as never)}</Badge>
                    {c.pr ? (
                      <a href={c.pr} target="_blank" rel="noreferrer noopener" className="text-xs text-primary underline underline-offset-2">
                        PR
                      </a>
                    ) : null}
                  </div>
                  <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{c.prompt}</p>
                  {c.error ? (
                    <p className="mt-1 text-xs text-destructive">{c.error}</p>
                  ) : c.result ? (
                    <p className="mt-1 line-clamp-3 text-xs text-muted-foreground/80">{c.result}</p>
                  ) : null}
                  <p className="mt-1 font-mono text-[10px] text-muted-foreground">
                    {c.id} · {timeAgo(c.updated_at)}
                  </p>
                </div>
                {c.status === 'pending' || c.status === 'failed' ? (
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={t('common.remove')}
                    onClick={async () => {
                      await del(`/autopilot/${c.id}`)
                      reload()
                    }}
                    className="shrink-0 text-muted-foreground hover:text-destructive"
                  >
                    <Trash className="size-4" />
                  </Button>
                ) : null}
              </div>
            </Card>
          ))}
        </div>
      )}
    </PageLayout>
  )
}

function AddDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onAdded: () => void
}) {
  const { t } = useI18n()
  const [title, setTitle] = useState('')
  const [prompt, setPrompt] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (open) {
      setTitle('')
      setPrompt('')
    }
  }, [open])

  const save = async () => {
    if (!title.trim() || !prompt.trim()) return
    setBusy(true)
    try {
      await post('/autopilot', { title: title.trim(), prompt: prompt.trim() })
      onAdded()
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('autopilot.add')}</DialogTitle>
          <DialogDescription>{t('autopilot.addDesc')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="ap-title">{t('autopilot.cardTitle')}</Label>
            <Input id="ap-title" autoFocus value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t('autopilot.cardTitlePlaceholder')} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ap-prompt">{t('autopilot.cardPrompt')}</Label>
            <Textarea id="ap-prompt" rows={5} value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder={t('autopilot.cardPromptPlaceholder')} />
          </div>
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button size="sm" onClick={save} loading={busy} disabled={!title.trim() || !prompt.trim()}>
            {t('autopilot.add')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
