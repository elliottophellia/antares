export type ProviderCapability = 'llm' | 'agent'

export interface ProviderCapabilityInfo {
  id: string
  capability?: ProviderCapability
}

export function isAgentProvider(provider: Pick<ProviderCapabilityInfo, 'capability'>): boolean {
  return provider.capability === 'agent'
}

export function providerModelsPath(provider: ProviderCapabilityInfo): string | null {
  return isAgentProvider(provider)
    ? `/providers/${encodeURIComponent(provider.id)}/models`
    : null
}

export function agentModelsErrorText(
  response: { error?: string } | undefined,
  requestError: Error | undefined,
): string | undefined {
  return response?.error ?? requestError?.message
}
