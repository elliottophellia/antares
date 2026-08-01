import { describe, expect, test } from 'bun:test'
import { toolsetsOrEmpty } from './toolPayload.ts'

describe('tool payload normalization', () => {
  test('turns null or missing toolsets into an empty array', () => {
    expect(toolsetsOrEmpty(null)).toEqual([])
    expect(toolsetsOrEmpty(undefined)).toEqual([])
  })

  test('keeps valid toolset arrays', () => {
    const sets = ['default', 'coding']
    expect(toolsetsOrEmpty(sets)).toBe(sets)
    expect(toolsetsOrEmpty(sets).slice(0, 4)).toEqual(sets)
  })
})
