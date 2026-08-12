import { describe, expect, test } from 'bun:test'
import { agentModelsErrorText, isAgentProvider, providerModelsPath } from './providerCapabilities.ts'

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

  test('uses the embedded agent-model discovery error from a 200 response', () => {
    expect(agentModelsErrorText({ error: 'Cursor API key expired' }, undefined))
      .toBe('Cursor API key expired')
  })

  test('uses a thrown request error when model discovery has no embedded error', () => {
    expect(agentModelsErrorText({ models: [] }, new Error('Network unavailable')))
      .toBe('Network unavailable')
  })

  test('prefers the embedded response error deterministically', () => {
    expect(agentModelsErrorText(
      { error: 'Cursor API key expired' },
      new Error('Network unavailable'),
    )).toBe('Cursor API key expired')
  })
})
