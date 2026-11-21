import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import AccountPoolsView from '../AccountPoolsView.vue'
import { upstreamSitesAPI as api } from '@/api/admin/upstreamSites'

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({}) }))
vi.mock('@/api/admin/upstreamSites', () => ({
  upstreamSitesAPI: { list: vi.fn(), previewPoolStatus: vi.fn(), poolStatusExport: vi.fn() },
  accountPoolStatusExportPath: '/pool-status',
  siteError: (error: Error) => error.message,
}))

it('refreshes the visible preview in place and leaves the last successful page on a polling error', async () => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  let generation = 0
  vi.mocked(api.list).mockResolvedValue([])
  vi.mocked(api.poolStatusExport).mockResolvedValue({ enabled: false })
  vi.mocked(api.previewPoolStatus).mockImplementation(async (page, pageSize) => ({
    page, page_size: pageSize, pools: [{ id: 'pool', source: '', name: `sample ${++generation}`,
      updated: null, snapshot_available: true, total: 40, pages: 2, plans: {}, statuses: {}, rows: [] }],
  }))
  const wrapper = mount(AccountPoolsView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' }, ConfirmDialog: true, Icon: true,
    TotpStepUpDialog: true, UpstreamAccountPoolPanel: true, UpstreamPoolStatusForm: true,
  } } })
  try {
    await flushPromises()
    expect(wrapper.text()).toContain('sample 1')
    await wrapper.get('[aria-label="upstreamSites.accountPoolNext"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('2 / 2')
    await vi.advanceTimersByTimeAsync(20_000)
    expect(api.previewPoolStatus).toHaveBeenLastCalledWith(2, 30)
    expect(wrapper.text()).toContain('sample 3')
    vi.mocked(api.previewPoolStatus).mockRejectedValueOnce(new Error('temporary error'))
    await vi.advanceTimersByTimeAsync(20_000)
    expect(wrapper.text()).toContain('sample 3')
    expect(wrapper.text()).not.toContain('temporary error')
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(20_000)
    expect(api.previewPoolStatus).toHaveBeenCalledTimes(4)
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(api.previewPoolStatus).toHaveBeenCalledTimes(5)
    expect(wrapper.text()).toContain('sample 4')
  } finally {
    wrapper.unmount()
    visibility.mockRestore()
    vi.useRealTimers()
  }
})
