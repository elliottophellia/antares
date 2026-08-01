import { describe, expect, test } from 'bun:test'
import {
  groupStreamPatches,
  queueStreamDelta,
  shouldRefreshAfterAttach,
} from './chatStreamQueue.ts'

describe('stream patch batching', () => {
  test('coalesces thousands of adjacent replay deltas', () => {
    const queue = []
    for (let i = 0; i < 4000; i++) queueStreamDelta(queue, 'assistant', 'reasoning', 'x')

    expect(queue).toHaveLength(1)
    expect(queue[0]).toEqual({
      id: 'assistant',
      kind: 'delta',
      segment: 'reasoning',
      delta: 'x'.repeat(4000),
    })
  })

  test('preserves boundaries between messages, segments, and apply patches', () => {
    const queue = []
    queueStreamDelta(queue, 'a', 'text', 'one')
    queueStreamDelta(queue, 'a', 'reasoning', 'two')
    queueStreamDelta(queue, 'b', 'text', 'three')
    queue.push({ id: 'a', kind: 'apply', fn: (message) => ({ text: message.text + '!' }) })
    queueStreamDelta(queue, 'a', 'text', 'four')

    expect(queue).toHaveLength(5)
    const grouped = groupStreamPatches(queue)
    expect(grouped.get('a')).toHaveLength(4)
    expect(grouped.get('b')).toHaveLength(1)
  })
})

describe('attach hydration policy', () => {
  test('refreshes only the first idle attachment', () => {
    expect(shouldRefreshAfterAttach(false, false)).toBe(true)
    expect(shouldRefreshAfterAttach(false, true)).toBe(false)
  })

  test('always refreshes after a live stream emitted events', () => {
    expect(shouldRefreshAfterAttach(true, false)).toBe(true)
    expect(shouldRefreshAfterAttach(true, true)).toBe(true)
  })
})
