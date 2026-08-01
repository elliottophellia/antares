export type StreamSegment = 'text' | 'reasoning'

export type QueuedStreamPatch<T> =
  | { id: string; kind: 'apply'; fn: (message: T) => T }
  | { id: string; kind: 'delta'; segment: StreamSegment; delta: string }

/**
 * Add a text/reasoning delta while collapsing an adjacent run of compatible
 * deltas. A reconnect can replay thousands of token-sized events synchronously;
 * keeping those as individual closures makes the browser concatenate an
 * ever-growing string thousands of times before it can paint.
 */
export function queueStreamDelta<T>(
  queue: QueuedStreamPatch<T>[],
  id: string,
  segment: StreamSegment,
  delta: string,
): void {
  if (!delta) return
  const last = queue[queue.length - 1]
  if (last?.kind === 'delta' && last.id === id && last.segment === segment) {
    last.delta += delta
    return
  }
  queue.push({ id, kind: 'delta', segment, delta })
}

/** Group patches once before walking the transcript. */
export function groupStreamPatches<T>(
  queue: QueuedStreamPatch<T>[],
): Map<string, QueuedStreamPatch<T>[]> {
  const grouped = new Map<string, QueuedStreamPatch<T>[]>()
  for (const patch of queue) {
    const patches = grouped.get(patch.id)
    if (patches) patches.push(patch)
    else grouped.set(patch.id, [patch])
  }
  return grouped
}

/**
 * Hydrate after a completed live stream, and once after the first idle attach to
 * close the initial-load race. Repeated idle polls must not rebuild the whole
 * transcript every time.
 */
export function shouldRefreshAfterAttach(hadEvents: boolean, idleRefreshDone: boolean): boolean {
  return hadEvents || !idleRefreshDone
}
