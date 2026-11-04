import type { KeyModelMappingEntry } from './keyModelMapping'

export function buildKeyPromptPolicy(entries: KeyModelMappingEntry[]): Record<string, string> {
  if (entries.length > 100) throw new Error('Too many prompt policies')
  const policy: Record<string, string> = Object.create(null)
  for (const entry of entries) {
    const model = entry.from.trim()
    if (!model || model.length > 256 || Object.hasOwn(policy, model) || !entry.to || (entry.to.startsWith('custom:') && !entry.to.slice(7).trim())) throw new Error('Invalid prompt policy')
    policy[model] = entry.to
  }
  return policy
}
