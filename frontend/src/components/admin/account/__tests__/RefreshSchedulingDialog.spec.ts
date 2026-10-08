import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import RefreshSchedulingDialog from '../RefreshSchedulingDialog.vue'
import type { AdminGroup } from '@/types'
import { getGroupSchedulingStatus } from '@/api/admin/groups'

vi.mock('@/api/admin/groups', () => ({ getModelAllowlistCandidates: vi.fn().mockResolvedValue(['model-a']), getGroupSchedulingStatus: vi.fn() }))
vi.mock('@/api/client', () => ({ buildApiUrl: (path: string) => `/api/v1${path}` }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const group = { id: 71, name: 'Test group' } as AdminGroup
function mountDialog() {
  return mount(RefreshSchedulingDialog, { props: { group }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' } } } })
}
function response(events: object[]) {
  const data = events.map(event => `data: ${JSON.stringify(event)}\n\n`).join('')
  const bytes = new TextEncoder().encode(data)
  return new Response(new ReadableStream({ start(controller) {
    controller.enqueue(bytes.slice(0, 11))
    controller.enqueue(bytes.slice(11))
    controller.close()
  } }))
}
afterEach(() => vi.unstubAllGlobals())
describe('active scheduling evaluation', () => {
	 it('reads scheduling status without sending paid probes or changing the binding', async () => {
		const fetch = vi.fn()
		vi.stubGlobal('fetch', fetch)
		vi.mocked(getGroupSchedulingStatus).mockResolvedValue({ model: 'model-a', current_account_id: 1, minimum_samples: 10, transition: null, candidates: [{ account_id: 1, name: 'Primary', current: true, eligible: true, rank: 1, confidence: 'unknown', samples: 0, success_rate: null, recent_failures: 0, last_success_at: null, last_failure_at: null, cooldown_seconds: 0 }] })
		const wrapper = mount(RefreshSchedulingDialog, { props: { group, readOnly: true }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' } } } })
		await flushPromises()
		expect(getGroupSchedulingStatus).toHaveBeenCalledWith(71, 'model-a')
		expect(wrapper.text()).toContain('Primary')
		expect(wrapper.text()).toContain('admin.accounts.schedulingStatus.unknown')
		expect(wrapper.text()).not.toContain('100.0%')
		await wrapper.get('input').trigger('keydown.enter')
		await flushPromises()
		expect(fetch).not.toHaveBeenCalled()
		expect(wrapper.emitted('updated')).toBeUndefined()
		wrapper.unmount()
	 })
  it('starts only on explicit action and shows streamed results and the winner', async () => {
    const fetch = vi.fn().mockResolvedValue(response([
      { type: 'start', total: 2 }, { type: 'testing', account_id: 1, name: 'A' },
      { type: 'result', account_id: 1, name: 'A', status: 'success', first_output_ms: 123 },
      { type: 'result', account_id: 2, name: 'B', status: 'skipped', error: 'paused' },
      { type: 'complete', account_id: 1, name: 'A', model: 'model-a' }
    ]))
    vi.stubGlobal('fetch', fetch)
    const wrapper = mountDialog()
    await flushPromises()
    expect(fetch).not.toHaveBeenCalled()
    await wrapper.get('input').setValue(' model-a ')
    await wrapper.findAll('button')[1]!.trigger('click')
    await flushPromises()
    expect(fetch).toHaveBeenCalledWith('/api/v1/admin/groups/71/refresh-scheduling', expect.objectContaining({ body: JSON.stringify({ model: 'model-a' }) }))
    expect(wrapper.text()).toContain('123 ms')
    expect(wrapper.text()).toContain('paused')
    expect(wrapper.emitted('updated')).toHaveLength(1)
    expect(wrapper.findAll('button')[1]!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('does not report success on a truncated stream or backend error', async () => {
    for (const events of [[], [{ type: 'error', error: 'no successful account' }]]) {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(events)))
      const wrapper = mountDialog()
      await wrapper.get('input').setValue('model-a')
      await wrapper.findAll('button')[1]!.trigger('click')
      await flushPromises()
      expect(wrapper.find('[role="alert"]').exists()).toBe(true)
      expect(wrapper.emitted('updated')).toBeUndefined()
      wrapper.unmount()
    }
  })
  it('aborts the upstream evaluation when closed', async () => {
    let signal: AbortSignal | undefined
    vi.stubGlobal('fetch', vi.fn().mockImplementation((_url, init) => {
      signal = init.signal
      return new Promise((_resolve, reject) => signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    }))
    const wrapper = mountDialog()
    await wrapper.get('input').setValue('model-a')
    await wrapper.findAll('button')[1]!.trigger('click')
    await wrapper.findAll('button')[0]!.trigger('click')
    await flushPromises()
    expect(signal?.aborted).toBe(true)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.emitted('updated')).toBeUndefined()
    wrapper.unmount()
  })
})
