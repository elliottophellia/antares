import { useMemo, useState } from 'react'
import {
  CaretDown,
  CaretRight,
  CheckCircle,
  CircleNotch,
  Database,
  FilePlus,
  FileText,
  Globe,
  Image,
  ListChecks,
  MagnifyingGlass,
  PencilSimple,
  Terminal,
  Wrench,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import type { ToolCallView } from '@/pages/ChatPage'
import { useI18n } from '@/lib/i18n'

type IconType = React.ComponentType<{ className?: string; weight?: 'regular' | 'fill' }>
type Meta = { label: string; Icon: IconType }

// Verb + icon per tool, so a call reads as an action ("edit foo.ts") rather than
// a raw function name. The verb is implied by the icon/diff, mirroring how a
// clean IDE chat presents its tools.
const TOOL_META: Record<string, Meta> = {
  read_file: { label: 'read', Icon: FileText },
  write_file: { label: 'create', Icon: FilePlus },
  edit_file: { label: 'edit', Icon: PencilSimple },
  list_files: { label: 'list', Icon: FileText },
  glob: { label: 'find', Icon: MagnifyingGlass },
  grep: { label: 'search', Icon: MagnifyingGlass },
  terminal: { label: 'run', Icon: Terminal },
  web_search: { label: 'search', Icon: Globe },
  web_fetch: { label: 'fetch', Icon: Globe },
  memory: { label: 'memory', Icon: Database },
  rag_search: { label: 'recall', Icon: Database },
  rag_index: { label: 'index', Icon: Database },
  session_search: { label: 'search', Icon: MagnifyingGlass },
  todo: { label: 'tasks', Icon: ListChecks },
  view_image: { label: 'view image', Icon: Image },
}

type DiffRow = { type: 'ctx' | 'add' | 'del'; text: string }
type Diff = { rows: DiffRow[]; added: number; removed: number }

// Line-based diff (LCS) between old and new source, for the expanded edit view.
function lineDiff(oldText: string, newText: string): Diff {
  const a = (oldText || '').replace(/\n$/, '').split('\n')
  const b = (newText || '').replace(/\n$/, '').split('\n')
  if (!oldText) {
    return { rows: b.map((text) => ({ type: 'add', text })), added: b.length, removed: 0 }
  }
  const n = a.length
  const m = b.length
  const dp = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const rows: DiffRow[] = []
  let i = 0
  let j = 0
  let added = 0
  let removed = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      rows.push({ type: 'ctx', text: a[i] })
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      rows.push({ type: 'del', text: a[i] })
      i++
      removed++
    } else {
      rows.push({ type: 'add', text: b[j] })
      j++
      added++
    }
  }
  while (i < n) {
    rows.push({ type: 'del', text: a[i] })
    i++
    removed++
  }
  while (j < m) {
    rows.push({ type: 'add', text: b[j] })
    j++
    added++
  }
  return { rows, added, removed }
}

/** One-line human summary of a tool call, derived from its arguments. */
function summarize(name: string, args: Record<string, unknown>): string {
  const s = (k: string) => (typeof args[k] === 'string' ? (args[k] as string) : '')
  switch (name) {
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
    case 'view_image': {
      const p = s('path')
      return p ? p.split('/').pop() || p : ''
    }
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

  const args = useMemo<Record<string, unknown>>(() => {
    try {
      return JSON.parse(call.args || '{}') as Record<string, unknown>
    } catch {
      return {}
    }
  }, [call.args])

  const meta = TOOL_META[call.name] ?? { label: call.name, Icon: Wrench }
  const isEdit = call.name === 'edit_file'
  const isCreate = call.name === 'write_file'
  const isDiff = isEdit || isCreate
  const isRead = call.name === 'read_file'
  const output = call.result ?? call.progress ?? ''

  // Only the file tools render as filename-over-directory. Other tools may also
  // carry a `path` arg (e.g. view_image with a URL), but for them that is just
  // an argument — they get the name-header + summary layout like everything else.
  const FILE_TOOLS = new Set(['read_file', 'write_file', 'edit_file', 'list_files'])
  const path = FILE_TOOLS.has(call.name) && typeof args.path === 'string' ? (args.path as string) : ''
  const fileName = path ? path.split('/').pop() || path : ''
  const parentPath = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''
  const summary = path ? '' : summarize(call.name, args)

  const diff = useMemo<Diff>(() => {
    if (isEdit) return lineDiff(String(args.old_string ?? ''), String(args.new_string ?? ''))
    if (isCreate) return lineDiff('', String(args.content ?? ''))
    return { rows: [], added: 0, removed: 0 }
  }, [isEdit, isCreate, args.old_string, args.new_string, args.content])

  // Lines read, for the collapsed read summary.
  const readLines = isRead && output.trim() ? output.trim().split('\n').length : 0

  const canExpand = isDiff ? diff.rows.length > 0 : (call.args && call.args !== '{}') || !!output

  // Right-side status: a diff tally for edits, "N lines" for reads, a spinner
  // while running, else a short result echo.
  const right = call.isError ? (
    <span className="text-[10px] font-semibold text-destructive">{t('chat.toolError')}</span>
  ) : isDiff ? (
    <span className="flex items-center gap-1.5 text-[10px] font-semibold tabular-nums">
      {call.running ? <CircleNotch className="size-3 animate-spin text-muted-foreground" /> : null}
      {diff.added > 0 ? <span className="text-emerald-500">+{diff.added}</span> : null}
      {diff.removed > 0 ? <span className="text-destructive">-{diff.removed}</span> : null}
    </span>
  ) : call.running ? (
    <CircleNotch className="size-3.5 animate-spin text-muted-foreground" />
  ) : isRead && readLines ? (
    <span className="text-[10px] text-muted-foreground/70">{t('chat.nLines', { n: readLines })}</span>
  ) : call.result !== undefined ? (
    output ? (
      <span className="max-w-[45%] truncate text-[10px] text-muted-foreground/70">
        {output.trim().split('\n')[0]}
      </span>
    ) : (
      <CheckCircle className="size-3.5 text-[var(--success)]" weight="fill" />
    )
  ) : null

  return (
    <div
      className={cn(
        'overflow-hidden rounded-[var(--radius-sm)] border bg-card',
        call.isError ? 'border-destructive/40' : 'border-border',
      )}
    >
      <button
        type="button"
        onClick={() => canExpand && setOpen((v) => !v)}
        className={cn(
          'flex w-full items-start gap-2 px-2.5 py-1.5 text-left',
          canExpand ? 'cursor-pointer hover:bg-muted/40' : 'cursor-default',
        )}
      >
        <span className="mt-0.5 shrink-0 text-muted-foreground">
          {canExpand ? (
            open ? (
              <CaretDown className="size-3.5" />
            ) : (
              <CaretRight className="size-3.5" />
            )
          ) : (
            <meta.Icon className="size-3.5" />
          )}
        </span>

        {path ? (
          <span className="flex min-w-0 flex-1 flex-col leading-tight" title={path}>
            <span
              className={cn(
                'truncate font-mono text-[11px]',
                isDiff ? 'font-medium text-primary' : 'text-foreground',
              )}
            >
              {fileName}
            </span>
            {parentPath ? (
              <span className="truncate font-mono text-[10px] text-muted-foreground/60">
                {parentPath}/
              </span>
            ) : null}
          </span>
        ) : (
          // Name as a header, with the argument summary on its own line beneath
          // — so a long summary (e.g. a fetched URL or a notice) never crowds
          // the name or gets squeezed on one line.
          <span className="flex min-w-0 flex-1 flex-col gap-0.5 leading-tight">
            <span className="truncate font-mono text-[11px] font-medium text-foreground">
              {meta.label}
            </span>
            {summary ? (
              <span className="truncate font-mono text-[10px] text-muted-foreground">
                {summary}
              </span>
            ) : null}
          </span>
        )}

        <span className="mt-0.5 shrink-0">{right}</span>
      </button>

      {open && canExpand ? (
        isDiff ? (
          <div className="border-t border-border bg-muted/30">
            <div className="max-h-64 overflow-auto py-1 font-mono text-[11px] leading-[1.5]">
              {diff.rows.map((r, ri) => (
                <div
                  key={ri}
                  className={cn(
                    'flex gap-2 px-3',
                    r.type === 'add'
                      ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
                      : r.type === 'del'
                        ? 'bg-destructive/10 text-destructive'
                        : 'text-muted-foreground/70',
                  )}
                >
                  <span
                    className={cn(
                      'select-none',
                      r.type === 'add'
                        ? 'text-emerald-500'
                        : r.type === 'del'
                          ? 'text-destructive'
                          : 'text-transparent',
                    )}
                  >
                    {r.type === 'add' ? '+' : r.type === 'del' ? '-' : ' '}
                  </span>
                  <span className="whitespace-pre">{r.text || ' '}</span>
                </div>
              ))}
            </div>
          </div>
        ) : (
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
        )
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
