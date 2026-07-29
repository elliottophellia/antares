import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import {
  ArrowUp,
  Brain,
  CaretDown,
  Check,
  Copy,
  FileText,
  Paperclip,
  Plus,
  Stop,
  Terminal,
  Warning,
  X,
} from '@phosphor-icons/react'
import { ApiError, get, post, streamGet, streamPost, type StreamEvent } from '@/lib/api'
import { copyText } from '@/lib/clipboard'
import { useI18n, useTimeAgo, type MessageKey } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/primitives'
import { SkeletonMessage } from '@/components/ui/skeleton'
import { Markdown } from '@/components/chat/Markdown'
import { ToolCallCard } from '@/components/chat/ToolCallCard'
import { TaskBar, parseTasks } from '@/components/chat/TaskBar'
import { ApprovalCard, type ApprovalView } from '@/components/chat/ApprovalCard'
import { AskUserCard } from '@/components/chat/AskUserCard'
import { RolePicker } from '@/components/chat/RolePicker'
import { ModelPicker } from '@/components/chat/ModelPicker'
import { SubAgentPanel, type ActiveAgent } from '@/components/chat/SubAgentPanel'
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
  // Non-image attachments shown as chips under the user's message.
  docs?: { path: string; name: string }[]
}

/** Append a text or reasoning delta, extending the last segment when it is the
 *  same kind so a streamed sentence stays one block. */
export function appendSeg(m: ChatMessage, kind: 'text' | 'reasoning', delta: string): ChatMessage {
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

export function pushToolSeg(m: ChatMessage, call: ToolCallView): ChatMessage {
  return {
    ...m,
    segments: [...(m.segments ?? []), { kind: 'tool', call }],
    toolCalls: [...(m.toolCalls ?? []), call],
  }
}

export function updateToolSeg(m: ChatMessage, id: string, fn: (c: ToolCallView) => ToolCallView): ChatMessage {
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
    hidden?: boolean
  }>
}

/** Rebuild view models from the persisted message log. */
function hydrate(detail: SessionDetail): ChatMessage[] {
  const out: ChatMessage[] = []
  const pending = new Map<string, ToolCallView>()

  for (const m of detail.messages) {
    // Hidden messages (e.g. an injected sub-agent result) are context for the
    // model, not something to render — the agent's continuation shows instead.
    if (m.hidden) continue
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
  // Live status for the streaming indicator: which step, and what tool (if any)
  // is running right now. Reset at the start of every send.
  const [live, setLive] = useState<{ turn: number; tool?: string; waiting?: boolean }>({ turn: 1 })
  const [input, setInput] = useState('')
  const [error, setError] = useState<string>()
  const [title, setTitle] = useState('')
  const [approvals, setApprovals] = useState<ApprovalView[]>([])
  // ask_user pauses the turn until answered. We keep the waiting ask's id (from
  // the `ask` event) so the card posts the answer to /api/asks/{id} — which
  // resumes the SAME turn — rather than sending a new chat message.
  const [askId, setAskId] = useState<string | undefined>()
  // Data URLs, which is what the API takes and what a preview needs.
  const [images, setImages] = useState<string[]>([])
  // Non-image attachments, uploaded to a temp dir. The agent reads them with
  // the read_document tool via the path we hand it on send.
  const [docs, setDocs] = useState<{ path: string; name: string }[]>([])
  const [role, setRole] = useState('')
  // When set, an overlay shows this sub-agent's live transcript instead of the
  // main one; clearing it returns to the main agent.
  const [viewingAgent, setViewingAgent] = useState<ActiveAgent | null>(null)

  const commands = useCommands()
  const matches = useMatches(input, commands)
  const [paletteSel, setPaletteSel] = useState(0)

  // The current checklist is the most recent todo write anywhere in the
  // transcript; it drives the sticky task bar above the composer.
  const tasks = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const calls = messages[i].toolCalls
      if (!calls) continue
      for (let j = calls.length - 1; j >= 0; j--) {
        if (calls[j].name === 'todo') {
          const items = parseTasks(calls[j].args)
          if (items.length > 0) return items
        }
      }
    }
    return []
  }, [messages])

  const abortRef = useRef<(() => void) | null>(null)
  // Holds the id of a session that was just created mid-stream on this page.
  // Its messages are already live on screen, so the hydrate effect must not
  // re-fetch and overwrite them before the turn is persisted.
  const localSessionRef = useRef<string | null>(null)
  // The current session id, tracked in a ref so a message always posts to the
  // right session even before the URL param has caught up. Without this, the
  // first reply creates a session but the next message — sent before the param
  // re-render lands — posts with an empty id and starts a second session.
  const sessionIdRef = useRef<string | undefined>(sessionId)
  useEffect(() => {
    sessionIdRef.current = sessionId
  }, [sessionId])
  const virtuosoRef = useRef<VirtuosoHandle>(null)
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

  useEffect(() => setPaletteSel(0), [input])

  // Leaving the page closes our stream connection, but the turn runs detached on
  // the server, so it keeps going and we reattach to it on return.
  useEffect(() => () => abortRef.current?.(), [])

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

  // Apply one stream event to the named assistant message. Shared by a fresh
  // send and a reattach, so both render a turn identically. Session handling
  // differs between the two (navigate vs. title-only), so it is delegated.
  const applyEvent = useCallback(
    (
      assistantId: string,
      event: StreamEvent,
      onSession?: (id: string, title?: string) => void,
    ) => {
      const patchAssistant = (fn: (m: ChatMessage) => ChatMessage) =>
        setMessages((prev) => prev.map((m) => (m.id === assistantId ? fn(m) : m)))
      switch (event.type) {
        case 'session':
          onSession?.(
            typeof event.id === 'string' ? event.id : '',
            typeof event.title === 'string' ? event.title : undefined,
          )
          break
        case 'text':
          patchAssistant((m) => appendSeg(m, 'text', String(event.delta ?? '')))
          break
        case 'reasoning':
          patchAssistant((m) => appendSeg(m, 'reasoning', String(event.delta ?? '')))
          break
        case 'tool_call':
          setLive((s) => ({ turn: s.turn + 1, tool: String(event.name ?? '') }))
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
          setLive((s) => ({ ...s, tool: undefined }))
          patchAssistant((m) =>
            updateToolSeg(m, String(event.id ?? ''), (c) => ({
              ...c,
              result: String(event.content ?? ''),
              isError: !!event.is_error,
              running: false,
            })),
          )
          break
        case 'ask':
          // The turn is now paused inside ask_user. Remember the id so the
          // answer card can resume it; the stream stays open (no 'done').
          setAskId(String(event.id ?? ''))
          setLive((s) => ({ ...s, tool: undefined, waiting: true }))
          break
        case 'usage':
          patchAssistant((m) => ({
            ...m,
            tokensIn: Number(event.input_tokens ?? m.tokensIn ?? 0),
            tokensOut: Number(event.output_tokens ?? m.tokensOut ?? 0),
          }))
          break
        case 'reset':
          // The turn is being retried after a provider glitch — throw away the
          // partial reply so the retry does not render on top of it.
          patchAssistant((m) => ({
            ...m,
            content: '',
            reasoning: undefined,
            toolCalls: undefined,
            segments: [],
            error: undefined,
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
    [t],
  )

  // Reconnect to a turn still running for this session (after navigating away
  // and back). Replays from the given cursor, so no tokens are missed, and
  // builds the assistant message lazily — if nothing is live, the server says
  // done at once and no empty bubble appears. Returns a closer.
  //
  // The done handler ALWAYS re-hydrates the persisted session, regardless of
  // whether any event arrived. That covers the race where the turn finished
  // between the user navigating away and back: the initial hydrate in the
  // outer useEffect ran against a stale DB snapshot (the assistant message
  // is persisted at end-of-turn), and the attach's `done` is the earliest
  // moment we know the canonical state is available. Skipping the re-fetch
  // when no event came (the previous behaviour) left the chat showing the
  // pre-turn state — the symptom that looked like "session disappeared".
  const attachLive = useCallback(
    (sid: string) => {
      // A standing attachment: after a turn ends we reconnect, so a turn the
      // SERVER starts later — a background sub-agent finishing and waking the
      // main agent — streams in live without a refresh. `alive` gates the loop
      // so the cleanup truly stops it.
      let alive = true
      let close: (() => void) | undefined
      let assistantId: string | null = null

      const connect = () => {
        if (!alive) return
        // Never run the standing attach while a foreground send is streaming:
        // that turn already renders via streamPost, and a second follower would
        // double-render it. Retry shortly instead.
        if (abortRef.current) {
          window.setTimeout(connect, 1500)
          return
        }
        assistantId = null
        const ensure = () => {
          if (assistantId) return assistantId
          assistantId = `live_${Date.now()}_a`
          setMessages((prev) => [
            ...prev,
            { id: assistantId as string, role: 'assistant', content: '' },
          ])
          setStreaming(true)
          setLive({ turn: 1 })
          return assistantId
        }
        close = streamGet(
          `/chat/attach?session_id=${encodeURIComponent(sid)}&cursor=0`,
          (event) => {
            if (event.type === 'done') {
              setStreaming(false)
              close?.() // stop EventSource from auto-reconnecting
              // Swap whatever we have for the canonical persisted turn.
              get<SessionDetail>(`/sessions/${sid}`)
                .then((d) => {
                  setMessages(hydrate(d))
                  setTitle(d.session.title || t('chat.conversation'))
                })
                .catch(() => {})
              // Reconnect shortly so a server-initiated wake turn is not missed.
              // The attach returns 'done' at once when nothing is live, so this
              // is a light poll of "is a new turn streaming?" — one open SSE, not
              // a request loop.
              if (alive) window.setTimeout(connect, 1500)
              return
            }
            applyEvent(ensure(), event, (_id, evtTitle) => {
              if (evtTitle) setTitle(evtTitle)
            })
          },
          () => {
            setStreaming(false)
            close?.()
            if (alive) window.setTimeout(connect, 3000)
          },
        )
      }

      connect()
      return () => {
        alive = false
        close?.()
      }
    },
    [applyEvent, t],
  )

  useEffect(() => {
    if (!sessionId) {
      setMessages([])
      setTitle('')
      setLoading(false)
      return
    }
    // A brand-new chat navigates to its own url mid-stream. The live messages
    // are already on screen; re-fetching now would find the turn not yet
    // persisted and wipe them. Skip the hydrate for that one session.
    if (sessionId === localSessionRef.current) {
      setLoading(false)
      return
    }
    setLoading(true)
    let cancelled = false
    let closeAttach: (() => void) | undefined
    get<SessionDetail>(`/sessions/${sessionId}`)
      .then((d) => {
        if (cancelled) return
        setMessages(hydrate(d))
        setTitle(d.session.title || t('chat.conversation'))
        setError(undefined)
        // Once the persisted history is on screen, reconnect to any turn still
        // in flight for this session so streaming continues where it left off.
        closeAttach = attachLive(sessionId)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        // The session does not exist (e.g. a stale "last conversation" pointer
        // to a session that was deleted). Forget it and drop to a fresh chat
        // instead of getting stuck on a blank, dead url.
        if (e instanceof ApiError && e.status === 404) {
          if (localStorage.getItem('antares:last-session') === sessionId) {
            localStorage.removeItem('antares:last-session')
          }
          setMessages([])
          setTitle('')
          setError(undefined)
          navigate('/', { replace: true, state: { fresh: true } })
          return
        }
        setError(e instanceof Error ? e.message : String(e))
      })
    get<{ role?: string }>(`/sessions/${sessionId}/role`)
      .then((r) => setRole(r.role ?? ''))
      .catch(() => {})
      .finally(() => setLoading(false))
    // t is stable per language; refetching on language change is harmless.
    return () => {
      cancelled = true
      closeAttach?.()
    }
  }, [sessionId, t, attachLive])

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
          session_id: sessionIdRef.current ?? '',
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
            if (last) await copyText(last.content)
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

  // sendText posts a message directly, bypassing the composer input. Used both
  // by the composer (send) and by inline answer buttons (e.g. ask_user options).
  const sendText = useCallback(
    (raw: string, attached: string[] = [], attachedDocs: { path: string; name: string }[] = []) => {
      const text = raw.trim()
      if ((!text && attached.length === 0 && attachedDocs.length === 0) || streaming) return
      if (text.startsWith('/') && text.length > 1) {
        void runCommand(text)
        return
      }

    // Non-image attachments live in a temp dir; the model can't see them until
    // it reads them. Tell it they're there and how — read_document by path.
    let message = text
    if (attachedDocs.length > 0) {
      const list = attachedDocs.map((d) => `- ${d.name} (path: ${d.path})`).join('\n')
      const note = `Attached file(s) — read each with the read_document tool before answering:\n${list}`
      message = text ? `${text}\n\n${note}` : note
    }

    const userMsg: ChatMessage = {
      id: `local_${Date.now()}`,
      role: 'user',
      content: text,
      docs: attachedDocs.length > 0 ? attachedDocs : undefined,
    }
    const assistantId = `local_${Date.now()}_a`
    setMessages((prev) => [...prev, userMsg, { id: assistantId, role: 'assistant', content: '' }])
    setInput('')
    setImages([])
    setDocs([])
    setError(undefined)
    setStreaming(true)
    setLive({ turn: 1 })

    abortRef.current = streamPost(
      '/chat',
      { session_id: sessionIdRef.current ?? '', message, images: attached, role },
      (event: StreamEvent) => {
        // End-of-turn: stop streaming immediately rather than waiting for the
        // socket to close. A detached run keeps the connection open past the
        // final event, which otherwise left the indicator and the task bar
        // "running" forever.
        if (event.type === 'done') {
          setStreaming(false)
          setAskId(undefined)
          setLive((s) => ({ ...s, waiting: false }))
          abortRef.current?.()
          abortRef.current = null
          localSessionRef.current = null
          return
        }
        applyEvent(assistantId, event, (id, evtTitle) => {
          if (id) {
            // Adopt the real session id at once, so the next message posts to it
            // rather than opening another session.
            sessionIdRef.current = id
            if (id !== sessionId) {
              // The server assigned this id — either a brand-new chat, or the
              // one in the url was stale/missing so a fresh session was created.
              // Point the url at the real session (and remember it so the hydrate
              // the navigation triggers does not overwrite the live messages).
              localSessionRef.current = id
              localStorage.setItem('antares:last-session', id)
              navigate(`/c/${id}`, { replace: true })
            }
          }
          if (evtTitle) setTitle(evtTitle)
        })
      },
      (err) => {
        setError(err.message)
        setStreaming(false)
        abortRef.current = null
        // The turn is persisted now, so a later revisit should hydrate fresh.
        localSessionRef.current = null
      },
      () => {
        setStreaming(false)
        abortRef.current = null
        localSessionRef.current = null
      },
    )
    },
    [role, streaming, sessionId, navigate, runCommand, applyEvent, t],
  )

  const send = useCallback(() => {
    const text = input.trim()
    if ((!text && images.length === 0 && docs.length === 0) || streaming) return
    sendText(text, images, docs)
  }, [input, images, docs, streaming, sendText])

  // answerAsk delivers an ask_user answer to the paused turn. Unlike sending a
  // message, this resumes the SAME turn: the answer becomes the tool result and
  // the model keeps going. The stream is already open, so nothing restarts.
  const answerAsk = useCallback(
    (answer: string) => {
      const id = askId
      if (!id) return
      setAskId(undefined)
      setLive((s) => ({ ...s, waiting: false }))
      void post(`/asks/${encodeURIComponent(id)}`, { answer }).catch((e) => {
        setError((e as Error).message)
      })
    },
    [askId],
  )

  const complete = (c: CommandSpec) => {
    // Commands that take arguments keep the composer open on a trailing space;
    // ones that do not are ready to send.
    setInput(`/${c.name}${c.args ? ' ' : ''}`)
    textareaRef.current?.focus()
  }

  const readDataURL = (file: File) =>
    new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error)
      reader.readAsDataURL(file)
    })

  /**
   * Attach files. Images become data URLs (the vision API takes them inline).
   * Everything else is uploaded to a temp dir and tracked by path; the agent
   * reads it with the read_document tool via the path we hand it on send.
   */
  const attachFiles = useCallback(async (files: FileList | File[]) => {
    const all = Array.from(files)
    const imgs = all.filter((f) => f.type.startsWith('image/'))
    const others = all.filter((f) => !f.type.startsWith('image/'))

    if (imgs.length > 0) {
      const read = await Promise.all(imgs.slice(0, 4).map(readDataURL))
      setImages((prev) => [...prev, ...read].slice(0, 4))
    }

    for (const file of others.slice(0, 4)) {
      try {
        const dataURL = await readDataURL(file)
        const res = await post<{ path: string; name: string }>('/upload', {
          session_id: sessionIdRef.current ?? '',
          name: file.name,
          data: dataURL,
        })
        setDocs((prev) => [...prev, { path: res.path, name: res.name }].slice(0, 8))
      } catch (e) {
        setError((e as Error).message)
      }
    }
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

  // composerCard renders the input surface. When `withTasks` is set, the task
  // list is folded into the top of the same card (normal view); the empty state
  // passes it false so there is nothing to fold in.
  const composerCard = (withTasks: boolean) => (
    <>
      <SlashPalette matches={matches} selected={paletteSel} onPick={complete} />
      <Composer
        ref={textareaRef}
        value={input}
        images={images}
        docs={docs}
        onAttach={attachFiles}
        onRemoveImage={(i) => setImages((prev) => prev.filter((_, x) => x !== i))}
        onRemoveDoc={(i) => setDocs((prev) => prev.filter((_, x) => x !== i))}
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
        roleSlot={
          <div className="flex min-w-0 items-center gap-1.5">
            <RolePicker value={role} onChange={setRole} compact />
            <ModelPicker />
          </div>
        }
        topSlot={
          withTasks ? (
            <TaskBar tasks={tasks} live={streaming} onOpenSubAgent={setViewingAgent} />
          ) : undefined
        }
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

            {composerCard(false)}

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
    <div className="relative flex h-[calc(100dvh-8rem)] flex-col overflow-x-hidden lg:h-dvh">
      {/* Sub-agent live view: overlays the chat while keeping the main
          transcript state intact underneath, so "back to Main" is instant. */}
      {viewingAgent ? (
        <div className="absolute inset-0 z-20 bg-background">
          <SubAgentPanel agent={viewingAgent} onBack={() => setViewingAgent(null)} />
        </div>
      ) : null}

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

      {loading ? (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-3xl space-y-6 px-4 py-6 sm:px-6">
            <SkeletonMessage />
            <SkeletonMessage />
          </div>
        </div>
      ) : (
        // Virtualised transcript: only on-screen messages are in the DOM, so a
        // very long session stays light. followOutput="auto" keeps it pinned to
        // the newest message only while the user is at the bottom — scroll up
        // and it stops, scroll back and it resumes.
        <Virtuoso
          ref={virtuosoRef}
          className="min-h-0 flex-1"
          data={messages}
          followOutput="auto"
          initialTopMostItemIndex={Math.max(0, messages.length - 1)}
          computeItemKey={(_, m) => m.id}
          itemContent={(_, m) => (
            <div className="mx-auto w-full min-w-0 max-w-3xl overflow-x-clip px-4 sm:px-6">
              <div className="min-w-0 py-2.5">
                <MessageBubble message={m} askActive={!!askId} onAnswer={answerAsk} />
              </div>
            </div>
          )}
          components={{
            Footer: () => (
              <div className="mx-auto w-full max-w-3xl space-y-5 px-4 pb-6 sm:px-6">
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
                {streaming ? (
                  <StreamingIndicator turn={live.turn} tool={live.tool} waiting={live.waiting} />
                ) : null}
                {error ? <ErrorBanner message={error} /> : null}
              </div>
            ),
          }}
        />
      )}

      {/* Floating composer: sits close to the last message rather than pinned
          against the very bottom edge of the viewport. */}
      <div className="bg-gradient-to-t from-background via-background to-transparent px-4 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))] sm:px-6 sm:pb-[max(2rem,env(safe-area-inset-bottom))]">
        <div className="mx-auto w-full max-w-3xl">{composerCard(true)}</div>
      </div>
    </div>
  )
}

interface ComposerProps {
  value: string
  images: string[]
  docs: { path: string; name: string }[]
  onChange: (v: string) => void
  onAttach: (files: FileList | File[]) => void
  onRemoveImage: (index: number) => void
  onRemoveDoc: (index: number) => void
  onPaste: (e: React.ClipboardEvent) => void
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  onSend: () => void
  onStop: () => void
  streaming: boolean
  placeholder: string
  sendLabel: string
  stopLabel: string
  attachLabel: string
  // roleSlot renders on the left of the bottom control row (the role picker).
  roleSlot?: React.ReactNode
  // topSlot renders above the textarea inside the same card (the task list),
  // separated by a divider — so tasks and composer read as one surface.
  topSlot?: React.ReactNode
}

/** Rounded single-surface composer with the actions inside the field. */
const Composer = ({
  ref,
  value,
  images,
  docs,
  onChange,
  onAttach,
  onRemoveImage,
  onRemoveDoc,
  onPaste,
  onKeyDown,
  onSend,
  onStop,
  streaming,
  placeholder,
  sendLabel,
  stopLabel,
  attachLabel,
  roleSlot,
  topSlot,
}: ComposerProps & { ref: React.RefObject<HTMLTextAreaElement | null> }) => {
  const fileRef = useRef<HTMLInputElement>(null)

  return (
    // No overflow-hidden: the role picker's dropdown pops upward out of this
    // card, and clipping would cut it off. The top section rounds its own top
    // corners instead so the merged look survives without clipping.
    <div className="rounded-[var(--radius-xl)] border border-border bg-card shadow-sm transition-colors focus-within:border-ring">
      {/* Task list / sub-agents (when present) sit above the input, in the same
          card. The section renders its own bottom divider only when it actually
          has content, so an empty TaskBar leaves no phantom line. */}
      {topSlot ? (
        <div className="overflow-hidden rounded-t-[var(--radius-xl)]">{topSlot}</div>
      ) : null}

      <div className="p-2">
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

        {docs.length > 0 ? (
          <div className="mb-2 flex flex-wrap gap-2 px-1 pt-1">
            {docs.map((d, i) => (
              <div
                key={i}
                className="group flex max-w-56 items-center gap-1.5 rounded-[var(--radius-sm)] border border-border bg-muted/40 py-1 pl-2 pr-1 text-xs"
              >
                <FileText className="size-4 shrink-0 text-muted-foreground" />
                <span className="truncate" title={d.name}>
                  {d.name}
                </span>
                <button
                  onClick={() => onRemoveDoc(i)}
                  aria-label="Remove"
                  className="shrink-0 rounded-full p-0.5 text-muted-foreground transition-colors hover:text-destructive"
                >
                  <X className="size-3.5" weight="bold" />
                </button>
              </div>
            ))}
          </div>
        ) : null}

        <input
          ref={fileRef}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) onAttach(e.target.files)
            // Reset so picking the same file twice still fires.
            e.target.value = ''
          }}
        />

        {/* Row 1: the input, full width. */}
        <Textarea
          ref={ref}
          rows={1}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          placeholder={placeholder}
          className="max-h-50 min-h-9 w-full resize-none border-0 bg-transparent px-1.5 py-1.5 shadow-none outline-none focus-visible:border-0 focus-visible:outline-none focus-visible:ring-0 focus-visible:ring-offset-0"
        />

        {/* Row 2: controls — role on the left, attach + send on the right. */}
        <div className="mt-1 flex items-center gap-1.5">
          {roleSlot}
          <div className="min-w-0 flex-1" />
          <Button
            size="icon"
            variant="ghost"
            onClick={() => fileRef.current?.click()}
            aria-label={attachLabel}
            className="shrink-0 rounded-full text-muted-foreground"
          >
            <Paperclip className="size-5" />
          </Button>
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

export function StreamingIndicator({
  turn,
  tool,
  waiting,
}: {
  turn?: number
  tool?: string
  waiting?: boolean
}) {
  const { t } = useI18n()
  const [secs, setSecs] = useState(0)
  useEffect(() => {
    const start = Date.now()
    const id = setInterval(() => setSecs(Math.round((Date.now() - start) / 1000)), 1000)
    return () => clearInterval(id)
  }, [])
  // Paused on a question: no timer, no pulsing "working" — the run is idle by
  // design, waiting on the person. Otherwise show the running tool / step.
  if (waiting) {
    return (
      <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
        <span className="size-1.5 rounded-full bg-[var(--warning)]" />
        <span className="font-medium text-foreground/70">{t('chat.waitingAnswer')}</span>
      </div>
    )
  }
  const label = tool
    ? t('chat.running', { tool })
    : turn && turn > 1
      ? t('chat.workingStep', { n: turn })
      : t('chat.working')
  return (
    <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
      <span className="flex items-center gap-1">
        <span className="pulse-dot size-1.5 rounded-full bg-primary" />
        <span className="pulse-dot size-1.5 rounded-full bg-primary [animation-delay:0.2s]" />
        <span className="pulse-dot size-1.5 rounded-full bg-primary [animation-delay:0.4s]" />
      </span>
      <span className="font-medium text-foreground/70">{label}</span>
      <span className="text-[10px] tabular-nums text-muted-foreground/60">· {secs}s</span>
    </div>
  )
}

function ReasoningBlock({ text }: { text: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  // A slim inline toggle rather than a boxed card: collapsed reasoning should
  // barely take a line, expanding into a quiet left-ruled block when opened.
  return (
    <div className="text-muted-foreground">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-[11px] font-medium transition-colors hover:text-foreground"
      >
        <Brain className="size-3.5" />
        {t('chat.reasoning')}
        <CaretDown className={cn('size-3 transition-transform', open && 'rotate-180')} />
      </button>
      {open ? (
        <p className="mt-1.5 whitespace-pre-wrap break-words border-l-2 border-border pl-3 text-xs">
          {text}
        </p>
      ) : null}
    </div>
  )
}

// Memoised: a streaming turn mutates only the last message, but setMessages
// hands a new array each token. Without memo every bubble in a long transcript
// re-renders per token — the main source of lag. With stable props (message
// reference unchanged for old turns, onAnswer via useCallback), React skips
// them and only the changed bubble re-renders.
export const MessageBubble = memo(function MessageBubble({
  message,
  askActive,
  onAnswer,
}: {
  message: ChatMessage
  // Whether an ask_user question is still awaiting an answer. When false the
  // card locks (already answered, or the run ended).
  askActive?: boolean
  onAnswer?: (text: string) => void
}) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    if (await copyText(message.content)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  if (message.role === 'user') {
    // Full-width, quietly set apart with a left rule and a faint tint — the
    // model's replies own the column, the prompt sits above them as context.
    return (
      <div className="fade-up space-y-2">
        {message.images?.length ? (
          <div className="flex flex-wrap gap-2">
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
        {message.docs?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.docs.map((d, i) => (
              <div
                key={i}
                className="flex max-w-56 items-center gap-1.5 rounded-[var(--radius-sm)] border border-border bg-muted/40 px-2 py-1 text-xs"
              >
                <FileText className="size-4 shrink-0 text-muted-foreground" />
                <span className="truncate" title={d.name}>
                  {d.name}
                </span>
              </div>
            ))}
          </div>
        ) : null}
        {message.content ? (
          <div className="rounded-[var(--radius-md)] border-l-2 border-primary bg-muted/40 px-3.5 py-2.5">
            <p className="whitespace-pre-wrap break-words text-[13px] leading-relaxed text-foreground">
              {message.content}
            </p>
          </div>
        ) : null}
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
    <div className="group min-w-0 space-y-1.5 fade-up">
      {message.segments && message.segments.length > 0
        ? message.segments.map((seg, i) => {
            if (seg.kind === 'reasoning') {
              return <ReasoningBlock key={`r${i}`} text={seg.text} />
            }
            if (seg.kind === 'tool') {
              // todo calls surface in the sticky TaskBar, not inline.
              if (seg.call.name === 'todo') return null
              // ask_user renders as a question with clickable answers instead
              // of a raw tool card.
              if (seg.call.name === 'ask_user') {
                return (
                  <AskUserCard
                    key={seg.call.id}
                    call={seg.call}
                    disabled={!askActive}
                    onAnswer={onAnswer ?? (() => {})}
                  />
                )
              }
              return <ToolCallCard key={seg.call.id} call={seg.call} />
            }
            return (
              <div key={`t${i}`} className="text-[13px] leading-relaxed">
                <Markdown content={seg.text} />
              </div>
            )
          })
        : // Fallback for any message that predates the timeline model.
          <>
            {message.reasoning ? <ReasoningBlock text={message.reasoning} /> : null}
            {message.toolCalls?.map((call) =>
              call.name === 'todo' ? null : call.name === 'ask_user' ? (
                <AskUserCard key={call.id} call={call} disabled={!askActive} onAnswer={onAnswer ?? (() => {})} />
              ) : (
                <ToolCallCard key={call.id} call={call} />
              ),
            )}
            {message.content ? (
              <div className="text-[13px] leading-relaxed">
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
})
