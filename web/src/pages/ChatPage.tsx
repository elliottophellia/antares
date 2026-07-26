import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ArrowUp, Brain, CaretDown, Check, Copy, Plus, Stop, Warning } from '@phosphor-icons/react'
import { get, streamPost, type StreamEvent } from '@/lib/api'
import { useStickyScroll } from '@/lib/hooks'
import { useI18n, useTimeAgo, type MessageKey } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/primitives'
import { SkeletonMessage } from '@/components/ui/skeleton'
import { Markdown } from '@/components/chat/Markdown'
import { ToolCallCard } from '@/components/chat/ToolCallCard'

export interface ToolCallView {
  id: string
  name: string
  args: string
  result?: string
  isError?: boolean
  progress?: string
  running?: boolean
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'tool' | 'system'
  content: string
  reasoning?: string
  toolCalls?: ToolCallView[]
  createdAt?: string
  tokensIn?: number
  tokensOut?: number
  error?: string
}

interface SessionDetail {
  session: { id: string; title: string; model: string; provider: string }
  messages: Array<{
    id: string
    role: ChatMessage['role']
    content: string
    reasoning?: string
    tool_calls?: string
    tool_call_id?: string
    tool_name?: string
    created_at: string
    tokens_in: number
    tokens_out: number
  }>
}

/** Rebuild view models from the persisted message log. */
function hydrate(detail: SessionDetail): ChatMessage[] {
  const out: ChatMessage[] = []
  const pending = new Map<string, ToolCallView>()

  for (const m of detail.messages) {
    if (m.role === 'tool') {
      const call = pending.get(m.tool_call_id ?? '')
      if (call) {
        call.result = m.content
        call.running = false
      }
      continue
    }
    const msg: ChatMessage = {
      id: m.id,
      role: m.role,
      content: m.content,
      reasoning: m.reasoning || undefined,
      createdAt: m.created_at,
      tokensIn: m.tokens_in,
      tokensOut: m.tokens_out,
    }
    if (m.tool_calls) {
      try {
        const parsed = JSON.parse(m.tool_calls) as Array<{ id: string; name: string; arguments: string }>
        msg.toolCalls = parsed.map((c) => {
          const view: ToolCallView = { id: c.id, name: c.name, args: c.arguments }
          pending.set(c.id, view)
          return view
        })
      } catch {
        /* ignore malformed history */
      }
    }
    out.push(msg)
  }
  return out
}

const SUGGESTION_KEYS: MessageKey[] = [
  'chat.suggest1',
  'chat.suggest2',
  'chat.suggest3',
  'chat.suggest4',
]

export default function ChatPage() {
  const { sessionId } = useParams<{ sessionId: string }>()
  const navigate = useNavigate()
  const { t } = useI18n()

  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loading, setLoading] = useState(!!sessionId)
  const [streaming, setStreaming] = useState(false)
  const [input, setInput] = useState('')
  const [error, setError] = useState<string>()
  const [title, setTitle] = useState('')

  const abortRef = useRef<(() => void) | null>(null)
  const scrollRef = useStickyScroll(messages)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (!sessionId) {
      setMessages([])
      setTitle('')
      setLoading(false)
      return
    }
    setLoading(true)
    get<SessionDetail>(`/sessions/${sessionId}`)
      .then((d) => {
        setMessages(hydrate(d))
        setTitle(d.session.title || t('chat.conversation'))
        setError(undefined)
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
    // t is stable per language; refetching on language change is harmless.
  }, [sessionId, t])

  // Grow the composer with its content, up to ~8 rows.
  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [input])

  const stop = useCallback(() => {
    abortRef.current?.()
    abortRef.current = null
    setStreaming(false)
  }, [])

  const send = useCallback(() => {
    const text = input.trim()
    if (!text || streaming) return

    const userMsg: ChatMessage = { id: `local_${Date.now()}`, role: 'user', content: text }
    const assistantId = `local_${Date.now()}_a`
    setMessages((prev) => [...prev, userMsg, { id: assistantId, role: 'assistant', content: '' }])
    setInput('')
    setError(undefined)
    setStreaming(true)

    const patchAssistant = (fn: (m: ChatMessage) => ChatMessage) =>
      setMessages((prev) => prev.map((m) => (m.id === assistantId ? fn(m) : m)))

    abortRef.current = streamPost(
      '/chat',
      { session_id: sessionId ?? '', message: text },
      (event: StreamEvent) => {
        switch (event.type) {
          case 'session':
            if (!sessionId && typeof event.id === 'string') {
              navigate(`/c/${event.id}`, { replace: true })
            }
            if (typeof event.title === 'string' && event.title) setTitle(event.title)
            break
          case 'text':
            patchAssistant((m) => ({ ...m, content: m.content + String(event.delta ?? '') }))
            break
          case 'reasoning':
            patchAssistant((m) => ({ ...m, reasoning: (m.reasoning ?? '') + String(event.delta ?? '') }))
            break
          case 'tool_call':
            patchAssistant((m) => ({
              ...m,
              toolCalls: [
                ...(m.toolCalls ?? []),
                {
                  id: String(event.id ?? ''),
                  name: String(event.name ?? ''),
                  args: String(event.arguments ?? ''),
                  running: true,
                },
              ],
            }))
            break
          case 'tool_progress':
            patchAssistant((m) => ({
              ...m,
              toolCalls: (m.toolCalls ?? []).map((c) =>
                c.id === event.id
                  ? { ...c, progress: (c.progress ?? '') + String(event.chunk ?? event.message ?? '') }
                  : c,
              ),
            }))
            break
          case 'tool_result':
            patchAssistant((m) => ({
              ...m,
              toolCalls: (m.toolCalls ?? []).map((c) =>
                c.id === event.id
                  ? { ...c, result: String(event.content ?? ''), isError: !!event.is_error, running: false }
                  : c,
              ),
            }))
            break
          case 'usage':
            patchAssistant((m) => ({
              ...m,
              tokensIn: Number(event.input_tokens ?? m.tokensIn ?? 0),
              tokensOut: Number(event.output_tokens ?? m.tokensOut ?? 0),
            }))
            break
          case 'error':
            patchAssistant((m) => ({ ...m, error: String(event.error ?? t('chat.somethingWrong')) }))
            break
        }
      },
      (err) => {
        setError(err.message)
        setStreaming(false)
        abortRef.current = null
      },
      () => {
        setStreaming(false)
        abortRef.current = null
      },
    )
  }, [input, streaming, sessionId, navigate, t])

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      send()
    }
  }

  const newChat = () => {
    stop()
    navigate('/')
    setMessages([])
    setTitle('')
  }

  const composer = (
    <Composer
      ref={textareaRef}
      value={input}
      onChange={setInput}
      onKeyDown={onKeyDown}
      onSend={send}
      onStop={stop}
      streaming={streaming}
      placeholder={t('chat.placeholder')}
      sendLabel={t('chat.send')}
      stopLabel={t('chat.stop')}
    />
  )

  const isEmpty = !loading && messages.length === 0

  // Empty state mirrors the familiar centred layout: greeting, composer, then
  // starter prompts — no bottom-anchored bar on an otherwise blank page.
  if (isEmpty) {
    return (
      <div className="flex min-h-[calc(100dvh-8rem)] flex-col lg:min-h-dvh">
        <div className="flex flex-1 items-center justify-center px-4 py-10 sm:px-6">
          <div className="w-full max-w-3xl space-y-6">
            <div className="space-y-2 text-center">
              <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
                {t('chat.welcomeTitle')}
              </h1>
              <p className="mx-auto max-w-lg text-sm text-muted-foreground">{t('chat.welcomeDesc')}</p>
            </div>

            {composer}

            <div className="grid gap-2 sm:grid-cols-2">
              {SUGGESTION_KEYS.map((key) => (
                <button
                  key={key}
                  onClick={() => {
                    setInput(t(key))
                    textareaRef.current?.focus()
                  }}
                  className="rounded-[var(--radius-md)] border border-border bg-card px-3.5 py-3 text-left text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground sm:text-sm"
                >
                  {t(key)}
                </button>
              ))}
            </div>

            {error ? <ErrorBanner message={error} /> : null}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-[calc(100dvh-8rem)] flex-col lg:h-dvh">
      <div className="flex items-center gap-3 border-b border-border px-4 py-3 sm:px-6">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{title || t('chat.newConversation')}</p>
          {sessionId ? (
            <p className="truncate text-[11px] text-muted-foreground">
              {t('chat.session')} {sessionId.slice(0, 12)}
            </p>
          ) : null}
        </div>
        <Button variant="outline" size="sm" onClick={newChat} className="gap-1.5">
          <Plus className="size-4" />
          <span className="hidden sm:inline">{t('common.new')}</span>
        </Button>
      </div>

      <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-3xl px-4 py-6 sm:px-6">
          {loading ? (
            <div className="space-y-6">
              <SkeletonMessage />
              <SkeletonMessage />
            </div>
          ) : (
            <div className="space-y-6">
              {messages.map((m) => (
                <MessageBubble key={m.id} message={m} />
              ))}
              {streaming ? <StreamingIndicator /> : null}
            </div>
          )}

          {error ? <ErrorBanner className="mt-4" message={error} /> : null}
        </div>
      </div>

      {/* Floating composer: sits close to the last message rather than pinned
          against the very bottom edge of the viewport. */}
      <div className="safe-bottom bg-gradient-to-t from-background via-background to-transparent px-4 pb-4 pt-3 sm:px-6">
        <div className="mx-auto w-full max-w-3xl">{composer}</div>
      </div>
    </div>
  )
}

interface ComposerProps {
  value: string
  onChange: (v: string) => void
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  onSend: () => void
  onStop: () => void
  streaming: boolean
  placeholder: string
  sendLabel: string
  stopLabel: string
}

/** Rounded single-surface composer with the action button inside the field. */
const Composer = ({
  ref,
  value,
  onChange,
  onKeyDown,
  onSend,
  onStop,
  streaming,
  placeholder,
  sendLabel,
  stopLabel,
}: ComposerProps & { ref: React.RefObject<HTMLTextAreaElement | null> }) => (
  <div className="flex items-end gap-2 rounded-[var(--radius-xl)] border border-border bg-card p-2 shadow-sm transition-colors focus-within:border-ring">
    <Textarea
      ref={ref}
      rows={1}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={onKeyDown}
      placeholder={placeholder}
      className="max-h-50 min-h-9 resize-none border-0 bg-transparent px-2 py-1.5 shadow-none focus-visible:border-0"
    />
    {streaming ? (
      <Button size="icon" variant="destructive" onClick={onStop} aria-label={stopLabel} className="shrink-0 rounded-full">
        <Stop weight="fill" />
      </Button>
    ) : (
      <Button
        size="icon"
        onClick={onSend}
        disabled={!value.trim()}
        aria-label={sendLabel}
        className="shrink-0 rounded-full"
      >
        <ArrowUp weight="bold" />
      </Button>
    )}
  </div>
)

function ErrorBanner({ message, className }: { message: string; className?: string }) {
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive',
        className,
      )}
    >
      <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
      <span className="min-w-0 break-words">{message}</span>
    </div>
  )
}

function StreamingIndicator() {
  return (
    <div className="flex items-center gap-1.5 px-1 text-xs text-muted-foreground">
      <span className="pulse-dot size-1.5 rounded-full bg-primary" />
      <span className="pulse-dot size-1.5 rounded-full bg-primary [animation-delay:0.2s]" />
      <span className="pulse-dot size-1.5 rounded-full bg-primary [animation-delay:0.4s]" />
    </div>
  )
}

function MessageBubble({ message }: { message: ChatMessage }) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [copied, setCopied] = useState(false)
  const [showReasoning, setShowReasoning] = useState(false)

  const copy = async () => {
    await navigator.clipboard.writeText(message.content)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  if (message.role === 'user') {
    return (
      <div className="flex justify-end fade-up">
        <div className="max-w-[85%] rounded-[var(--radius-lg)] rounded-br-sm bg-primary px-3.5 py-2.5 text-sm text-primary-foreground">
          <p className="whitespace-pre-wrap break-words">{message.content}</p>
        </div>
      </div>
    )
  }

  return (
    <div className="group space-y-2 fade-up">
      {message.reasoning ? (
        <div className="rounded-[var(--radius-sm)] border border-border bg-muted/40">
          <button
            onClick={() => setShowReasoning((v) => !v)}
            className="flex w-full items-center gap-1.5 px-3 py-2 text-[11px] font-medium text-muted-foreground"
          >
            <Brain className="size-3.5" />
            {t('chat.reasoning')}
            <CaretDown className={cn('ml-auto size-3 transition-transform', showReasoning && 'rotate-180')} />
          </button>
          {showReasoning ? (
            <p className="whitespace-pre-wrap break-words px-3 pb-3 text-xs text-muted-foreground">
              {message.reasoning}
            </p>
          ) : null}
        </div>
      ) : null}

      {message.toolCalls?.map((call) => <ToolCallCard key={call.id} call={call} />)}

      {message.content ? (
        <div className="text-sm leading-relaxed">
          <Markdown content={message.content} />
        </div>
      ) : null}

      {message.error ? <ErrorBanner message={message.error} /> : null}

      {message.content ? (
        <div className="flex items-center gap-2 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          <Button variant="ghost" size="icon-sm" onClick={copy} aria-label={t('common.copy')}>
            {copied ? <Check className="size-3.5 text-[var(--success)]" /> : <Copy className="size-3.5" />}
          </Button>
          {message.tokensOut ? (
            <span className="text-[10px] text-muted-foreground">
              {t('chat.tokensOut', { n: message.tokensOut })}
            </span>
          ) : null}
          {message.createdAt ? (
            <span className="text-[10px] text-muted-foreground">{timeAgo(message.createdAt)}</span>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
