import { describe, expect, test } from 'bun:test'
import {
  changedFilesFromMessages,
  isIncompleteToolCall,
  isSuccessfulToolCall,
} from './toolCallState.ts'

describe('tool call completion', () => {
  test('requires a non-error result before treating a call as successful', () => {
    expect(isSuccessfulToolCall({ result: 'Created file' })).toBe(true)
    expect(isSuccessfulToolCall({ result: 'cannot write', isError: true })).toBe(false)
    expect(isSuccessfulToolCall({ running: false })).toBe(false)
  })

  test('identifies a persisted call without a result as incomplete', () => {
    expect(isIncompleteToolCall({ running: false })).toBe(true)
    expect(isIncompleteToolCall({ running: true })).toBe(false)
    expect(isIncompleteToolCall({ result: '' })).toBe(false)
    expect(isIncompleteToolCall({ result: 'cannot write', isError: true })).toBe(false)
  })
})

describe('changed files', () => {
  test('excludes failed and incomplete writes', () => {
    const files = changedFilesFromMessages([
      {
        toolCalls: [
          { name: 'write_file', args: '{"path":"failed.txt"}', result: 'cannot write', isError: true },
          { name: 'write_file', args: '{"path":"interrupted.txt"}' },
          { name: 'write_file', args: '{"path":"empty.txt"}', result: '' },
        ],
      },
    ])

    expect(files).toEqual([{ path: 'empty.txt', tool: 'write_file' }])
  })

  test('keeps only the newest successful write for each path', () => {
    const files = changedFilesFromMessages([
      { toolCalls: [{ name: 'write_file', args: '{"path":"same.txt"}', result: 'Created same.txt' }] },
      {
        toolCalls: [
          { name: 'edit_file', args: '{"path":"same.txt"}', result: 'Updated same.txt' },
          { name: 'edit_file', args: '{"path":"other.txt"}', result: 'Updated other.txt' },
        ],
      },
    ])

    expect(files).toEqual([
      { path: 'other.txt', tool: 'edit_file' },
      { path: 'same.txt', tool: 'edit_file' },
    ])
  })
})
