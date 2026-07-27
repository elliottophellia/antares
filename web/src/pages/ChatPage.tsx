import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import {
  ArrowUp,
  Brain,
  CaretDown,
  Check,
  Copy,
  Paperclip,
  Plus,
  Stop,
  Terminal,
  Warning,
  X,
} from '@phosphor-icons/react'
import { get, post, streamPost, type StreamEvent } from '@/lib/api'
import { useStickyScroll } from '@/lib/hooks'
import { useI18n, useTimeAgo, type MessageKey } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/primitives'
import { SkeletonMessage } from '@/components/ui/skeleton'
import { Markdown } from '@/components/chat/Markdown'
import { ToolCallCard } from '@/components/chat/ToolCallCard'
import { ApprovalCard, type ApprovalView } from '@/components/chat/ApprovalCard'
import { RolePicker } from '@/components/chat/RolePicker'
import {
  SlashPalette,
  useCommands,
  useMatches,
  type CommandSpec,
} from '@/components/chat/SlashPalette'

export interface ToolCallView {
  id: string
  name: string
  args: string
  result?: string
  isError?: boolean
  progress?: string
  running?: boolean
}

/** One part of an assistant turn, in the order it happened. */
export type Segment =
  | { kind: 'text'; text: string }
  | { kind: 'reasoning'; text: string }
  | { kind: 'tool'; call: ToolCallView }

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'tool' | 'system'
  content: string
  reasoning?: string
  toolCalls?: ToolCallView[]
  // segments is the timeline: text, reasoning, and tool calls interleaved as
  // they arrived, so the transcript reads in the order the model worked.
  segments?: Segment[]
  createdAt?: string
  tokensIn?: number
  tokensOut?: number
  error?: string
  images?: string[]
}

/** Append a text or reasoning delta, extending the last segment when it is the
 *  same kind so a streamed sentence stays one block. */
function appendSeg(m: ChatMessage, kind: 'text' | 'reasoning', delta: string): ChatMessage {
  const segs = m.segments ? [...m.segments] : []
  const last = segs[segs.length - 1]
  if (last && last.kind === kind) {
    segs[segs.length - 1] = { kind, text: last.text + delta }
  } else {
    segs.push({ kind, text: delta })
  }
  return {
    ...m,
    segments: segs,
    content: kind === 'text' ? m.content + delta : m.content,
    reasoning: kind === 'reasoning' ? (m.reasoning ?? '') + delta : m.reasoning,
  }
}

function pushToolSeg(m: ChatMessage, call: ToolCallView): ChatMessage {
  return {
    ...m,
    segments: [...(m.segments ?? []), { kind: 'tool', call }],
    toolCalls: [...(m.toolCalls ?? []), call],
  }
}

function updateToolSeg(m: ChatMessage, id: string, fn: (c: ToolCallView) => ToolCallView): ChatMessage {
  return {
    ...m,
    segments: (m.segments ?? []).map((seg) =>
      seg.kind === 'tool' && seg.call.id === id ? { kind: 'tool', call: fn(seg.call) } : seg,
    ),
    toolCalls: (m.toolCalls ?? []).map((c) => (c.id === id ? fn(c) : c)),
  }
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
    attachments?: string
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
    // Images sent with the message are stored as the same parts the model saw.
    if (m.attachments) {
      try {
        const parts = JSON.parse(m.attachments) as Array<{
          mime_type?: string
          data?: string
          url?: string
        }>
        const srcs = parts
          .map((p) => p.url || (p.data ? `data:${p.mime_type || 'image/png'};base64,${p.data}` : ''))
          .filter(Boolean)
        if (srcs.length > 0) msg.images = srcs
      } catch {
        /* ignore malformed history */
      }
    }
    const segments: Segment[] = []
    if (msg.reasoning) segments.push({ kind: 'reasoning', text: msg.reasoning })
    if (msg.content) segments.push({ kind: 'text', text: msg.content })
    if (m.tool_calls) {
      try {
        const parsed = JSON.parse(m.tool_calls) as Array<{
          id: string
          name: string
          arguments: string
        }>
        msg.toolCalls = parsed.map((c) => {
          const view: ToolCallView = { id: c.id, name: c.name, args: c.arguments }
          pending.set(c.id, view)
          segments.push({ kind: 'tool', call: view })
          return view
        })
      } catch {
        /* ignore malformed history */
      }
    }
    if (msg.role === 'assistant' && segments.length > 0) msg.segments = segments
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
  const location = useLocation()
  const { t } = useI18n()

  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loading, setLoading] = useState(!!sessionId)
  const [streaming, setStreaming] = useState(false)
  const [input, setInput] = useState('')
  const [error, setError] = useState<string>()
  const [title, setTitle] = useState('')
  const [approvals, setApprovals] = useState<ApprovalView[]>([])
  // Data URLs, which is what the API takes and what a preview needs.
  const [images, setImages] = useState<string[]>([])
  const [role, setRole] = useState('')

  const commands = useCommands()
  const matches = useMatches(input, commands)
  const [paletteSel, setPaletteSel] = useState(0)

  const abortRef = useRef<(() => void) | null>(null)
  const scrollRef = useStickyScroll(messages)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Landing on a bare "/" resumes the last conversation, so switching back to
  // Chat continues where you were rather than starting over. The New button
  // arrives with state.fresh set, which skips the resume and forgets it.
  useEffect(() => {
    if (sessionId) {
      localStorage.setItem('antares:last-session', sessionId)
      return
    }
    if (location.state?.fresh) {
      localStorage.removeItem('antares:last-session')
      return
    }
    const last = localStorage.getItem('antares:last-session')
    if (last) navigate(`/c/${last}`, { replace: true })
    // Only when the route id changes, not on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId])

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
    get<{ role?: string }>(`/sessions/${sessionId}/role`)
      .then((r) => setRole(r.role ?? ''))
      .catch(() => {})
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
    // t is stable per language; refetching on language change is harmless.
  }, [sessionId, t])

  useEffect(() => setPaletteSel(0), [input])

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

  /** Append a locally-produced message without touching the server. */
  const pushSystem = useCallback((content: string) => {
    setMessages((prev) => [
      ...prev,
      { id: `cmd_${Date.now()}_${prev.length}`, role: 'system', content },
    ])
  }, [])

  /**
   * Slash commands are answered by the server rather than the model, so /status
   * costs nothing and returns instantly. A few of them only the browser can
   * carry out; those come back as an action.
   */
  const runCommand = useCallback(
    async (line: string) => {
      setInput('')
      setError(undefined)
      setMessages((prev) => [...prev, { id: `you_${Date.now()}`, role: 'user', content: line }])
      try {
        const r = await post<{
          ok: boolean
          error?: string
          output?: string
          action?: { kind?: string; value?: string }
        }>('/commands/run', {
          input: line,
          session_id: sessionId ?? '',
          surface: 'web',
        })

        if (!r.ok) {
          pushSystem(r.error ?? t('chat.somethingWrong'))
          return
        }

        switch (r.action?.kind) {
          case 'new':
          case 'clear':
            stop()
            localStorage.removeItem('antares:last-session')
            setMessages([])
            setTitle('')
            setApprovals([])
            navigate('/', { state: { fresh: true } })
            return
          case 'resume':
            if (r.action.value) navigate(`/c/${r.action.value}`)
            return
          case 'setup':
            navigate('/config')
            return
          case 'stop':
            stop()
            break
          case 'copy': {
            const last = [...messages].reverse().find((m) => m.role === 'assistant')
            if (last) await navigator.clipboard.writeText(last.content).catch(() => {})
            pushSystem(last ? t('chat.copied') : t('chat.nothingToCopy'))
            return
          }
          case 'retry': {
            const last = [...messages].reverse().find((m) => m.role === 'user')
            if (last) setInput(last.content)
            return
          }
        }
        if (r.output) pushSystem(r.output)
      } catch (e) {
        setError((e as Error).message)
      }
    },
    [sessionId, messages, navigate, pushSystem, stop, t],
  )

  const send = useCallback(() => {
    const text = input.trim()
    if ((!text && images.length === 0) || streaming) return
    if (text.startsWith('/') && text.length > 1) {
      void runCommand(text)
      return
    }
    const attached = images

    const userMsg: ChatMessage = {
      id: `local_${Date.now()}`,
      role: 'user',
      content: text,
    }
    const assistantId = `local_${Date.now()}_a`
    setMessages((prev) => [...prev, userMsg, { id: assistantId, role: 'assistant', content: '' }])
    setInput('')
    setImages([])
    setError(undefined)
    setStreaming(true)

    const patchAssistant = (fn: (m: ChatMessage) => ChatMessage) =>
      setMessages((prev) => prev.map((m) => (m.id === assistantId ? fn(m) : m)))

    abortRef.current = streamPost(
      '/chat',
      { session_id: sessionId ?? '', message: text, images: attached, role },
      (event: StreamEvent) => {
        switch (event.type) {
          case 'session':
            if (!sessionId && typeof event.id === 'string') {
              navigate(`/c/${event.id}`, { replace: true })
            }
            if (typeof event.title === 'string' && event.title) setTitle(event.title)
            break
          case 'text':
            patchAssistant((m) => appendSeg(m, 'text', String(event.delta ?? '')))
            break
          case 'reasoning':
            patchAssistant((m) => appendSeg(m, 'reasoning', String(event.delta ?? '')))
            break
          case 'tool_call':
            patchAssistant((m) =>
              pushToolSeg(m, {
                id: String(event.id ?? ''),
                name: String(event.name ?? ''),
                args: String(event.arguments ?? ''),
                running: true,
              }),
            )
            break
          case 'tool_progress':
            patchAssistant((m) =>
              updateToolSeg(m, String(event.id ?? ''), (c) => ({
                ...c,
                progress: (c.progress ?? '') + String(event.chunk ?? event.message ?? ''),
              })),
            )
            break
          case 'tool_result':
            patchAssistant((m) =>
              updateToolSeg(m, String(event.id ?? ''), (c) => ({
                ...c,
                result: String(event.content ?? ''),
                isError: !!event.is_error,
                running: false,
              })),
            )
            break
          case 'usage':
            patchAssistant((m) => ({
              ...m,
              tokensIn: Number(event.input_tokens ?? m.tokensIn ?? 0),
              tokensOut: Number(event.output_tokens ?? m.tokensOut ?? 0),
            }))
            break
          case 'error':
            patchAssistant((m) => ({
              ...m,
              error: String(event.error ?? t('chat.somethingWrong')),
            }))
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
  }, [input, images, role, streaming, sessionId, navigate, runCommand, t])

  const complete = (c: CommandSpec) => {
    // Commands that take arguments keep the composer open on a trailing space;
    // ones that do not are ready to send.
    setInput(`/${c.name}${c.args ? ' ' : ''}`)
    textareaRef.current?.focus()
  }

  /** Read files into data URLs, which is what both the preview and the API want. */
  const attachFiles = useCallback(async (files: FileList | File[]) => {
    const picked = Array.from(files).filter((f) => f.type.startsWith('image/'))
    if (picked.length === 0) return
    const read = await Promise.all(
      picked.slice(0, 4).map(
        (file) =>
          new Promise<string>((resolve, reject) => {
            const reader = new FileReader()
            reader.onload = () => resolve(String(reader.result))
            reader.onerror = () => reject(reader.error)
            reader.readAsDataURL(file)
          }),
      ),
    )
    setImages((prev) => [...prev, ...read].slice(0, 4))
  }, [])

  // Pasting a screenshot is the fastest way to show the agent something.
  const onPaste = (e: React.ClipboardEvent) => {
    const files = Array.from(e.clipboardData.files)
    if (files.length > 0) {
      e.preventDefault()
      void attachFiles(files)
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (matches.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setPaletteSel((i) => (i + 1) % matches.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setPaletteSel((i) => (i - 1 + matches.length) % matches.length)
        return
      }
      if (e.key === 'Tab') {
        e.preventDefault()
        complete(matches[paletteSel])
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setInput('')
        return
      }
      // Enter completes a partial name but sends one that is already whole,
      // so typing a full command and pressing Enter does what it looks like.
      if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
        const typed = input.slice(1).toLowerCase()
        if (!commands.some((c) => c.name === typed)) {
          e.preventDefault()
          complete(matches[paletteSel])
          return
        }
      }
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      send()
    }
  }

  const newChat = () => {
    stop()
    localStorage.removeItem('antares:last-session')
    setMessages([])
    setTitle('')
    setRole('')
    navigate('/', { state: { fresh: true } })
  }

  const composer = (
    <>
      <SlashPalette matches={matches} selected={paletteSel} onPick={complete} />
      <Composer
        ref={textareaRef}
        value={input}
        role={role}
        onRoleChange={setRole}
        images={images}
        onAttach={attachFiles}
        onRemoveImage={(i) => setImages((prev) => prev.filter((_, x) => x !== i))}
        onPaste={onPaste}
        onChange={setInput}
        onKeyDown={onKeyDown}
        onSend={send}
        onStop={stop}
        streaming={streaming}
        placeholder={t('chat.placeholder')}
        sendLabel={t('chat.send')}
        stopLabel={t('chat.stop')}
        attachLabel={t('chat.attach')}
      />
    </>
  )

  const isEmpty = !loading && messages.length === 0

  // Empty state mirrors the familiar centred layout: greeting, composer, then
  // starter prompts — no bottom-anchored bar on an otherwise blank page.
  if (isEmpty) {
    return (
      <div className="flex min-h-[calc(100dvh-8rem)] flex-col lg:min-h-dvh">
        <div className="flex flex-1 items-center justify-center px-4 py-10 sm:px-6">
          <div className="w-full max-w-3xl space-y-6">
            <div className="space-y-3 text-center">
              <img
                src="/antares-192.png"
                alt=""
                aria-hidden
                width={64}
                height={64}
                className="mx-auto size-16 select-none object-contain"
                draggable={false}
              />
              <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
                {t('chat.welcomeTitle')}
              </h1>
              <p className="mx-auto max-w-lg text-sm text-muted-foreground">
                {t('chat.welcomeDesc')}
              </p>
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
              {approvals.map((a) => (
                <ApprovalCard
                  key={a.id}
                  approval={a}
                  onDecided={(id, decision) =>
                    setApprovals((prev) =>
                      prev.map((x) => (x.id === id ? { ...x, decided: decision } : x)),
                    )
                  }
                />
              ))}
              {streaming ? <StreamingIndicator /> : null}
            </div>
          )}

          {error ? <ErrorBanner className="mt-4" message={error} /> : null}
        </div>
      </div>

      {/* Floating composer: sits close to the last message rather than pinned
          against the very bottom edge of the viewport. */}
      <div className="bg-gradient-to-t from-background via-background to-transparent px-4 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))] sm:px-6 sm:pb-[max(2rem,env(safe-area-inset-bottom))]">
        <div className="mx-auto w-full max-w-3xl">{composer}</div>
      </div>
    </div>
  )
}

interface ComposerProps {
  value: string
  role: string
  onRoleChange: (role: string) => void
  images: string[]
  onChange: (v: string) => void
  onAttach: (files: FileList | File[]) => void
  onRemoveImage: (index: number) => void
  onPaste: (e: React.ClipboardEvent) => void
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  onSend: () => void
  onStop: () => void
  streaming: boolean
  placeholder: string
  sendLabel: string
  stopLabel: string
  attachLabel: string
}

/** Rounded single-surface composer with the actions inside the field. */
const Composer = ({
  ref,
  value,
  role,
  onRoleChange,
  images,
  onChange,
  onAttach,
  onRemoveImage,
  onPaste,
  onKeyDown,
  onSend,
  onStop,
  streaming,
  placeholder,
  sendLabel,
  stopLabel,
  attachLabel,
}: ComposerProps & { ref: React.RefObject<HTMLTextAreaElement | null> }) => {
  const fileRef = useRef<HTMLInputElement>(null)

  return (
    <div className="rounded-[var(--radius-xl)] border border-border bg-card p-2 shadow-sm transition-colors focus-within:border-ring">
      {images.length > 0 ? (
        <div className="mb-2 flex flex-wrap gap-2 px-1 pt-1">
          {images.map((src, i) => (
            <div key={i} className="group relative">
              <img
                src={src}
                alt=""
                className="size-16 rounded-[var(--radius-sm)] border border-border object-cover"
              />
              <button
                onClick={() => onRemoveImage(i)}
                aria-label="Remove"
                className="absolute -right-1.5 -top-1.5 rounded-full bg-background p-0.5 text-muted-foreground shadow ring-1 ring-border transition-colors hover:text-destructive"
              >
                <X className="size-3.5" weight="bold" />
              </button>
            </div>
          ))}
        </div>
      ) : null}

      <div className="flex items-end gap-1.5">
        <RolePicker value={role} onChange={onRoleChange} />
        <input
          ref={fileRef}
          type="file"
          accept="image/*"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) onAttach(e.target.files)
            // Reset so picking the same file twice still fires.
            e.target.value = ''
          }}
        />
        <Button
          size="icon"
          variant="ghost"
          onClick={() => fileRef.current?.click()}
          aria-label={attachLabel}
          className="shrink-0 rounded-full text-muted-foreground"
        >
          <Paperclip className="size-5" />
        </Button>

        <Textarea
          ref={ref}
          rows={1}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          placeholder={placeholder}
          className="max-h-50 min-h-9 resize-none border-0 bg-transparent px-1 py-1.5 shadow-none focus-visible:border-0"
        />

        {streaming ? (
          <Button
            size="icon"
            variant="destructive"
            onClick={onStop}
            aria-label={stopLabel}
            className="shrink-0 rounded-full"
          >
            <Stop weight="fill" />
          </Button>
        ) : (
          <Button
            size="icon"
            onClick={onSend}
            disabled={!value.trim() && images.length === 0}
            aria-label={sendLabel}
            className="shrink-0 rounded-full"
          >
            <ArrowUp weight="bold" />
          </Button>
        )}
      </div>
    </div>
  )
}

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

function ReasoningBlock({ text }: { text: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-[var(--radius-sm)] border border-border bg-muted/40">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 px-3 py-2 text-[11px] font-medium text-muted-foreground"
      >
        <Brain className="size-3.5" />
        {t('chat.reasoning')}
        <CaretDown className={cn('ml-auto size-3 transition-transform', open && 'rotate-180')} />
      </button>
      {open ? (
        <p className="whitespace-pre-wrap break-words px-3 pb-3 text-xs text-muted-foreground">{text}</p>
      ) : null}
    </div>
  )
}

function MessageBubble({ message }: { message: ChatMessage }) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    await navigator.clipboard.writeText(message.content)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  if (message.role === 'user') {
    return (
      <div className="flex justify-end fade-up">
        <div className="max-w-[85%] space-y-2">
          {message.images?.length ? (
            <div className="flex flex-wrap justify-end gap-2">
              {message.images.map((src, i) => (
                <img
                  key={i}
                  src={src}
                  alt=""
                  className="max-h-48 rounded-[var(--radius-md)] border border-border object-contain"
                />
              ))}
            </div>
          ) : null}
          {message.content ? (
            <div className="rounded-[var(--radius-lg)] rounded-br-sm bg-primary px-3.5 py-2.5 text-sm text-primary-foreground">
              <p className="whitespace-pre-wrap break-words">{message.content}</p>
            </div>
          ) : null}
        </div>
      </div>
    )
  }

  // Slash-command output is not the model talking. Setting it apart keeps the
  // transcript honest about what came from where.
  if (message.role === 'system') {
    return (
      <div className="fade-up rounded-[var(--radius-md)] border border-border bg-muted/40 px-3.5 py-3">
        <div className="mb-1.5 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
          <Terminal className="size-3" />
          {t('chat.command')}
        </div>
        <div className="text-xs">
          <Markdown content={message.content} />
        </div>
      </div>
    )
  }

  return (
    <div className="group space-y-2 fade-up">
      {message.segments && message.segments.length > 0
        ? message.segments.map((seg, i) => {
            if (seg.kind === 'reasoning') {
              return <ReasoningBlock key={`r${i}`} text={seg.text} />
            }
            if (seg.kind === 'tool') {
              return <ToolCallCard key={seg.call.id} call={seg.call} />
            }
            return (
              <div key={`t${i}`} className="text-sm leading-relaxed">
                <Markdown content={seg.text} />
              </div>
            )
          })
        : // Fallback for any message that predates the timeline model.
          <>
            {message.reasoning ? <ReasoningBlock text={message.reasoning} /> : null}
            {message.toolCalls?.map((call) => (
              <ToolCallCard key={call.id} call={call} />
            ))}
            {message.content ? (
              <div className="text-sm leading-relaxed">
                <Markdown content={message.content} />
              </div>
            ) : null}
          </>}

      {message.error ? <ErrorBanner message={message.error} /> : null}

      {message.content ? (
        <div className="flex items-center gap-2 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          <Button variant="ghost" size="icon-sm" onClick={copy} aria-label={t('common.copy')}>
            {copied ? (
              <Check className="size-3.5 text-[var(--success)]" />
            ) : (
              <Copy className="size-3.5" />
            )}
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
