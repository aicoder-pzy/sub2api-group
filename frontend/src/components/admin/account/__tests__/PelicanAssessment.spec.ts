import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PelicanAssessment from '../PelicanAssessment.vue'

const api = vi.hoisted(() => ({ lookup: vi.fn(), start: vi.fn(), poll: vi.fn() }))
vi.mock('@/api/admin/pelicanAssessments', () => ({ pelicanAssessmentsAPI: api }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const html = '<!doctype html><html><body><svg></svg></body></html>'
const completed = (quality = 'normal') => ({ hash: 'abc', status: 'succeeded', assessment: { quality, reason: '<img onerror="alert(1)">', source: 'classifier:test', checked_at: '2026-10-09T00:00:00Z' } })
let wrapper: ReturnType<typeof mount>
beforeEach(() => {
  vi.useFakeTimers()
  for (const mock of Object.values(api)) mock.mockReset()
  api.lookup.mockResolvedValue(null)
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('HTML-only artwork assessment', () => {
  it('looks up existing data without uploading and submits raw HTML only on click', async () => {
    api.start.mockResolvedValue({ hash: 'abc', status: 'running' })
    api.poll.mockResolvedValue(completed('degraded'))
    wrapper = mount(PelicanAssessment, { props: { output: `Explanation\n\`\`\`html\n${html}\n\`\`\`` } })
    await flushPromises()
    expect(api.lookup).toHaveBeenCalledWith(html, expect.any(AbortSignal))
    expect(api.start).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="assess-pelican"]').trigger('click')
    expect(api.start).toHaveBeenCalledWith(html, expect.any(AbortSignal))
    expect(api.start.mock.calls[0][0]).not.toContain('Content-Security-Policy')
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(wrapper.get('[data-testid="assessment-quality"]').text()).toBe('pelicanTests.assessment.degraded')
    await wrapper.get('button').trigger('click')
    expect(wrapper.get('[data-testid="assessment-details"]').text()).toContain('<img onerror="alert(1)">')
    expect(wrapper.find('img').exists()).toBe(false)
  })

  it('restores saved assessments and treats service failures as unknown', async () => {
    api.lookup.mockResolvedValue(completed('unknown'))
    wrapper = mount(PelicanAssessment, { props: { output: html } })
    await flushPromises()
    expect(wrapper.text()).toContain('pelicanTests.assessment.unknown')
    expect(api.start).not.toHaveBeenCalled()
    api.lookup.mockResolvedValue(null)
    await wrapper.setProps({ output: '<html><body>new artwork</body></html>' })
    await flushPromises()
    api.start.mockRejectedValue(new Error('service unavailable'))
    await wrapper.get('[data-testid="assess-pelican"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('pelicanTests.assessment.unknown')
    expect(wrapper.text()).not.toContain('pelicanTests.assessment.degraded')
  })

  it('ignores old artwork responses and cancels polling after closing', async () => {
    let resolveOld!: (value: unknown) => void
    api.start.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    wrapper = mount(PelicanAssessment, { props: { output: html } })
    await flushPromises()
    await wrapper.get('[data-testid="assess-pelican"]').trigger('click')
    await wrapper.setProps({ output: '<html><body>new</body></html>' })
    await flushPromises()
    resolveOld(completed('degraded'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('pelicanTests.assessment.degraded')
    api.start.mockResolvedValue({ hash: 'new-hash', status: 'running' })
    await wrapper.get('[data-testid="assess-pelican"]').trigger('click')
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.poll).not.toHaveBeenCalled()
    expect(api.start.mock.calls[1][1].aborted).toBe(true)
  })

  it('refuses oversized source before any lookup or upload', async () => {
    wrapper = mount(PelicanAssessment, { props: { output: '<html>'+ 'x'.repeat(2 * 1024 * 1024) +'</html>' } })
    await flushPromises()
    expect(wrapper.text()).toContain('pelicanTests.assessment.tooLarge')
    expect(api.lookup).not.toHaveBeenCalled()
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
  })

  it('respects the service retry delay before enabling another submission', async () => {
    api.start.mockRejectedValue({ metadata: { retry_after_seconds: '17' } })
    wrapper = mount(PelicanAssessment, { props: { output: html } })
    await flushPromises()
    await wrapper.get('[data-testid="assess-pelican"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    await vi.advanceTimersByTimeAsync(17000)
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
  })
})
