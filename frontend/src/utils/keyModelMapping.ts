export interface KeyModelMappingEntry { from: string; to: string }

export function keyModelMappingEntries(mapping?: Record<string, string> | null): KeyModelMappingEntry[] {
  return Object.entries(mapping || {}).map(([from, to]) => ({ from, to }))
}

export function buildKeyModelMapping(entries: KeyModelMappingEntry[]): Record<string, string> {
  if (entries.length > 100) throw new Error('keys.modelMappingInvalid')
  const result: Record<string, string> = Object.create(null)
  const valid = (model: string) => model.length > 0 && new TextEncoder().encode(model).length <= 256 && !/[\s\p{Cc}*?#\\]/u.test(model)
  for (const { from, to } of entries) {
    const source = from.trim()
    const target = to.trim()
    if (!valid(source) || !valid(target) || Object.prototype.hasOwnProperty.call(result, source)) throw new Error('keys.modelMappingInvalid')
    result[source] = target
  }
  return result
}
