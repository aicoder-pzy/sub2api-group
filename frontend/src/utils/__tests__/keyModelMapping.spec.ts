import { describe, expect, it } from 'vitest'
import { buildKeyModelMapping, keyModelMappingEntries } from '../keyModelMapping'

describe('per-key model mappings', () => {
  it('loads, trims and clears mappings', () => {
    expect(keyModelMappingEntries({ alias: 'target' })).toEqual([{ from: 'alias', to: 'target' }])
    expect(buildKeyModelMapping([{ from: ' alias ', to: ' target ' }])).toEqual({ alias: 'target' })
    expect(buildKeyModelMapping([])).toEqual({})
  })
  it('rejects incomplete, duplicate and wildcard rules', () => {
    for (const entries of [
      [{ from: 'a', to: '' }], [{ from: '*', to: 'b' }], [{ from: 'a', to: 'b c' }],
      [{ from: 'a', to: 'b' }, { from: ' a ', to: 'c' }],
    ]) expect(() => buildKeyModelMapping(entries)).toThrow()
  })
  it('treats special object keys as plain model names', () => {
    const mapping = buildKeyModelMapping([{ from: '__proto__', to: 'target' }])
    expect(mapping.__proto__).toBe('target')
    expect(JSON.stringify(mapping)).toBe('{"__proto__":"target"}')
  })
})
