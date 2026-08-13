export interface ToolCallState {
  result?: string
  isError?: boolean
  running?: boolean
}

export interface FileToolCall extends ToolCallState {
  name: string
  args: string
}

export interface ToolCallMessage {
  toolCalls?: FileToolCall[]
}

/** A completed result is the only proof that a tool call changed the filesystem. */
export function isSuccessfulToolCall(call: ToolCallState): boolean {
  return !call.isError && call.result !== undefined
}

/** A persisted call without a completion record was interrupted mid-execution. */
export function isIncompleteToolCall(call: ToolCallState): boolean {
  return !call.running && call.result === undefined
}

/** Successful write/edit calls, newest first and de-duplicated by path. */
export function changedFilesFromMessages(messages: readonly ToolCallMessage[]): { path: string; tool: string }[] {
  const seen = new Set<string>()
  const files: { path: string; tool: string }[] = []
  for (let i = messages.length - 1; i >= 0; i--) {
    const calls = messages[i].toolCalls
    if (!calls) continue
    for (let j = calls.length - 1; j >= 0; j--) {
      const call = calls[j]
      if ((call.name !== 'write_file' && call.name !== 'edit_file') || !isSuccessfulToolCall(call)) continue
      try {
        const path = String(JSON.parse(call.args)?.path ?? '').trim()
        if (path && !seen.has(path)) {
          seen.add(path)
          files.push({ path, tool: call.name })
        }
      } catch {
        /* ignore unparseable arguments */
      }
    }
  }
  return files
}
