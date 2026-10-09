import { describe, expect, it, vi } from 'vitest'
import { pelicanAssessmentsAPI } from '../pelicanAssessments'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../client', () => ({ apiClient: client }))

describe('assessment requests never provide credentials to Manxue', () => {
  it('uses only our relative backend endpoints and an HTML-only body', async () => {
    client.post.mockResolvedValue({ data: null })
    const html = '<html><body>artwork</body></html>'
    await pelicanAssessmentsAPI.lookup(html)
    await pelicanAssessmentsAPI.start(html)
    expect(client.post.mock.calls).toEqual([
      ['/admin/pelican-assessments/lookup', { html }, { signal: undefined }],
      ['/admin/pelican-assessments', { html }, { signal: undefined }],
    ])
    client.get.mockResolvedValue({ data: null })
    await pelicanAssessmentsAPI.poll('a'.repeat(64))
    expect(client.get).toHaveBeenCalledWith('/admin/pelican-assessments/'+'a'.repeat(64), { signal: undefined })
  })
})
