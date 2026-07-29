import { useMemo, useState } from 'react'
import { Question, PaperPlaneTilt, ArrowLeft, ArrowRight, Check } from '@phosphor-icons/react'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/primitives'
import type { ToolCallView } from '@/pages/ChatPage'

interface AskQuestion {
  question: string
  header?: string
  options?: string[]
  multiSelect?: boolean
}

/** Accept both the single-question form and the questions[] array. */
function parseQuestions(raw: string): AskQuestion[] {
  try {
    const a = JSON.parse(raw) as {
      question?: string
      options?: string[]
      questions?: AskQuestion[]
    }
    if (Array.isArray(a.questions) && a.questions.length > 0) {
      return a.questions.filter((q) => q && q.question)
    }
    if (a.question) return [{ question: a.question, options: a.options }]
  } catch {
    /* fall through */
  }
  return []
}

/**
 * The agent called ask_user: it needs decisions only the person can make and
 * has stopped until they reply. Renders the questions one at a time with
 * next/previous and clickable options (plus free-text), then submits every
 * answer as one message so the run continues in a single turn.
 */
export function AskUserCard({
  call,
  disabled,
  onAnswer,
}: {
  call: ToolCallView
  disabled?: boolean
  onAnswer: (text: string) => void
}) {
  const { t } = useI18n()
  const questions = useMemo(() => parseQuestions(call.args), [call.args])
  const [idx, setIdx] = useState(0)
  const [answers, setAnswers] = useState<Record<number, string[]>>({})
  const [custom, setCustom] = useState('')
  const [submitted, setSubmitted] = useState(false)

  if (questions.length === 0) return null

  const q = questions[idx]
  const isLast = idx === questions.length - 1
  const multi = !!q.multiSelect
  const selected = answers[idx] ?? []

  const setSelected = (vals: string[]) => setAnswers((a) => ({ ...a, [idx]: vals }))

  const toggleOption = (o: string) => {
    if (multi) {
      setSelected(selected.includes(o) ? selected.filter((x) => x !== o) : [...selected, o])
    } else {
      // Single-select: pick, and for a one-question card that's the whole answer.
      setSelected([o])
      if (questions.length === 1) submit({ 0: [o] })
    }
  }

  const addCustom = () => {
    const v = custom.trim()
    if (!v) return
    setSelected(multi ? [...selected.filter((x) => x !== v), v] : [v])
    setCustom('')
    if (!multi && questions.length === 1) submit({ 0: [v] })
  }

  const answeredCount = questions.filter((_, i) => (answers[i]?.length ?? 0) > 0).length

  const submit = (final?: Record<number, string[]>) => {
    const all = final ?? answers
    const lines = questions.map((qq, i) => {
      const a = all[i]?.length ? all[i].join(', ') : t('ask.noAnswer')
      const label = qq.header || qq.question
      return `${label}: ${a}`
    })
    setSubmitted(true)
    onAnswer(questions.length === 1 ? (all[0]?.join(', ') ?? '') : lines.join('\n'))
  }

  if (submitted) {
    return (
      <div className="fade-up rounded-[var(--radius-md)] border border-primary/40 bg-primary/[0.06] p-3.5">
        <div className="flex items-start gap-2.5">
          <Check className="mt-0.5 size-5 shrink-0 text-primary" weight="bold" />
          <div className="min-w-0 flex-1 space-y-1">
            {questions.map((qq, i) => (
              <p key={i} className="text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{qq.header || qq.question}:</span>{' '}
                {answers[i]?.join(', ') || t('ask.noAnswer')}
              </p>
            ))}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="fade-up rounded-[var(--radius-md)] border border-primary/40 bg-primary/[0.06] p-3.5">
      <div className="flex items-start gap-2.5">
        <Question className="mt-0.5 size-5 shrink-0 text-primary" weight="fill" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            {q.header ? (
              <span className="rounded-full bg-primary/15 px-2 py-0.5 text-[11px] font-medium text-primary">
                {q.header}
              </span>
            ) : (
              <span />
            )}
            {questions.length > 1 ? (
              <span className="shrink-0 text-[11px] text-muted-foreground">
                {t('ask.progress', { current: idx + 1, total: questions.length })}
              </span>
            ) : null}
          </div>

          <p className="mt-1.5 whitespace-pre-wrap break-words text-sm font-medium">{q.question}</p>

          <div className="mt-3 space-y-2">
            {(q.options ?? []).length > 0 ? (
              <div className="flex flex-wrap gap-2">
                {q.options!.map((o) => {
                  const on = selected.includes(o)
                  return (
                    <Button
                      key={o}
                      size="sm"
                      variant={on ? 'secondary' : 'outline'}
                      disabled={disabled}
                      onClick={() => toggleOption(o)}
                      className={cn('max-w-full', on && 'border-primary')}
                    >
                      {on && multi ? <Check className="size-3.5" /> : null}
                      <span className="truncate">{o}</span>
                    </Button>
                  )
                })}
              </div>
            ) : null}

            {/* Free-text answer — always available (this is the "Other"). */}
            <div className="flex items-center gap-2">
              <Input
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault()
                    addCustom()
                  }
                }}
                placeholder={
                  (q.options ?? []).length > 0 ? t('ask.otherPlaceholder') : t('ask.customPlaceholder')
                }
                disabled={disabled}
              />
              <Button
                size="icon-sm"
                variant="outline"
                disabled={disabled || !custom.trim()}
                onClick={addCustom}
                aria-label={t('ask.add')}
              >
                <PaperPlaneTilt className="size-4" />
              </Button>
            </div>

            {multi && selected.length > 0 ? (
              <p className="text-[11px] text-muted-foreground">
                {t('ask.selected', { items: selected.join(', ') })}
              </p>
            ) : null}
          </div>

          {/* Navigation. A single question with options submits on click, so the
              nav row only matters for multi-question or free-text cases. */}
          {questions.length > 1 || multi || (q.options ?? []).length === 0 ? (
            <div className="mt-3 flex items-center justify-between gap-2">
              <Button
                size="sm"
                variant="ghost"
                disabled={disabled || idx === 0}
                onClick={() => setIdx((i) => Math.max(0, i - 1))}
                className="gap-1.5"
              >
                <ArrowLeft className="size-4" />
                {t('ask.prev')}
              </Button>
              {isLast ? (
                <Button
                  size="sm"
                  disabled={disabled || answeredCount === 0}
                  onClick={() => submit()}
                  className="gap-1.5"
                >
                  <Check className="size-4" />
                  {t('ask.submit')}
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={disabled}
                  onClick={() => setIdx((i) => Math.min(questions.length - 1, i + 1))}
                  className="gap-1.5"
                >
                  {t('ask.next')}
                  <ArrowRight className="size-4" />
                </Button>
              )}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}
