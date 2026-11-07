import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/api/client'
import { runSiteImports, type ImportDraft } from '../upstreamSites'

vi.mock('@/api/client', () => ({ apiClient: { post: vi.fn() } }))
const draft = (keyId: string): ImportDraft => ({ status: 'pending', idempotencyKey: `stable-${keyId}`, payload: { key_id: keyId, revision: 2, name: `account-${keyId}`, platform: 'openai', group_id: 5, concurrency: 3, rate_multiplier: 0.5, models: ['model-1'] } })
beforeEach(() => vi.resetAllMocks())
describe('upstream batch import', () => {
  it('keeps successes and retries an ambiguous failure with the same request ID and payload', async () => {
    const drafts = [draft('3'), draft('4')]
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { account_id: 100 } }).mockRejectedValueOnce({ message: 'timeout' })
    await runSiteImports('site', drafts, () => false)
    expect(drafts.map(item => item.status)).toEqual(['success', 'failed'])
    expect(drafts[1].error).toBe('timeout')
    const firstPayload = structuredClone(drafts[1].payload)
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { account_id: 101 } })
    await runSiteImports('site', drafts, () => false)
    expect(apiClient.post).toHaveBeenCalledTimes(3)
    expect(vi.mocked(apiClient.post).mock.calls[2][1]).toEqual(firstPayload)
    expect(vi.mocked(apiClient.post).mock.calls[1][2]?.headers).toEqual(vi.mocked(apiClient.post).mock.calls[2][2]?.headers)
    expect(drafts.map(item => item.status)).toEqual(['success', 'success'])
    expect(JSON.stringify(firstPayload)).not.toContain('api_key')
  })
  it('stops between keys without cancelling or replaying a completed write', async () => {
    const drafts = [draft('3'), draft('4')]
    let stopped = false
    vi.mocked(apiClient.post).mockImplementation(async () => { stopped = true; return { data: { account_id: 100 } } })
    await runSiteImports('site', drafts, () => stopped)
    expect(drafts.map(item => item.status)).toEqual(['success', 'pending'])
    expect(apiClient.post).toHaveBeenCalledTimes(1)
  })
})
