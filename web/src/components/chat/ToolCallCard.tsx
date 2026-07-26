import { useState } from 'react'
import {
  CaretDown,
  CheckCircle,
  CircleNotch,
  Database,
  FileText,
  Globe,
  ListChecks,
  MagnifyingGlass,
  PencilSimple,
  Terminal,
  Wrench,
  XCircle,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import type { ToolCallView } from '@/pages/ChatPage'
import { useI18n } from '@/lib/i18n'

const ICONS: Record<string, React.ComponentType<{ className?: string; weight?: 'regular' | 'fill' }>> = {
  read_file: FileText,
  write_file: PencilSimple,
  edit_file: PencilSimple,
  list_files: FileText,
  glob: MagnifyingGlass,
  grep: MagnifyingGlass,
  terminal: Terminal,
  web_search: Globe,
  web_fetch: Globe,
  memory: Database,
  rag_search: Database,
  rag_index: Database,
  session_search: MagnifyingGlass,
  todo: ListChecks,
}

/** One-line human summary of a tool call, derived from its arguments. */
function summarize(name: string, rawArgs: string): string {
  let args: Record<string, unknown> = {}
  try {
    args = JSON.parse(rawArgs || '{}') as Record<string, unknown>
  } catch {
    return ''
  }
  const s = (k: string) => (typeof args[k] === 'string' ? (args[k] as string) : '')
  switch (name) {
    case 'read_file':
    case 'write_file':
    case 'edit_file':
    case 'list_files':
      return s('path')
    case 'terminal':
      return s('command')
    case 'grep':
    case 'glob':
      return s('pattern')
    case 'web_search':
    case 'rag_search':
    case 'session_search':
      return s('query')
    case 'web_fetch':
      return s('url')
    case 'memory':
      return s('action') + (s('key') ? `: ${s('key')}` : '')
    case 'todo':
      return s('action')
    default:
      return Object.entries(args)
        .slice(0, 2)
        .map(([k, v]) => `${k}=${String(v).slice(0, 40)}`)
        .join(' ')
  }
}

export function ToolCallCard({ call }: { call: ToolCallView }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const Icon = ICONS[call.name] ?? Wrench
  const summary = summarize(call.name, call.args)
  const output = call.result ?? call.progress ?? ''

  return (
    <div
      className={cn(
        'overflow-hidden rounded-[var(--radius-sm)] border bg-card',
        call.isError ? 'border-destructive/40' : 'border-border',
      )}
    >
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
      >
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="shrink-0 font-mono text-[11px] font-medium">{call.name}</span>
        {summary ? (
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">
            {summary}
          </span>
        ) : (
          <span className="flex-1" />
        )}
        {call.running ? (
          <CircleNotch className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
        ) : call.isError ? (
          <XCircle className="size-3.5 shrink-0 text-destructive" weight="fill" />
        ) : call.result !== undefined ? (
          <CheckCircle className="size-3.5 shrink-0 text-[var(--success)]" weight="fill" />
        ) : null}
        <CaretDown className={cn('size-3 shrink-0 transition-transform', open && 'rotate-180')} />
      </button>

      {open ? (
        <div className="space-y-2 border-t border-border px-3 py-2">
          {call.args && call.args !== '{}' ? (
            <div>
              <p className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                {t('chat.toolArgs')}
              </p>
              <pre className="max-h-48 overflow-auto rounded bg-muted/50 p-2 font-mono text-[11px]">
                {prettyJSON(call.args)}
              </pre>
            </div>
          ) : null}
          {output ? (
            <div>
              <p className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                {t('chat.toolResult')}
              </p>
              <pre
                className={cn(
                  'max-h-80 overflow-auto whitespace-pre-wrap break-words rounded bg-muted/50 p-2 font-mono text-[11px]',
                  call.isError && 'text-destructive',
                )}
              >
                {output}
              </pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}
