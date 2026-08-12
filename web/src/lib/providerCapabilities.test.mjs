import { describe, expect, test } from 'bun:test'
import { isAgentProvider, providerModelsPath } from './providerCapabilities.ts'

describe('provider capabilities', () => {
  test('classifies Cursor as an agent integration', () => {
    expect(isAgentProvider({ capability: 'agent' })).toBe(true)
    expect(isAgentProvider({ capability: 'llm' })).toBe(false)
  })

  test('uses provider-specific models for agents only', () => {
    expect(providerModelsPath({ id: 'cursor', capability: 'agent' }))
      .toBe('/providers/cursor/models')
    expect(providerModelsPath({ id: 'openai', capability: 'llm' })).toBeNull()
  })
})
