import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import ChannelOperationsView from '../ChannelOperationsView.vue'
import * as channels from '@/api/admin/channels'
import * as ops from '@/api/admin/channelOperations'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/api/admin/channels', () => {
  const api = { list: vi.fn(), getById: vi.fn() }
  return { ...api, default: api }
})
vi.mock('@/api/admin/channelOperations', () => ({ catalog: vi.fn(), snapshot: vi.fn(), mutate: vi.fn() }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') })
}))
const section = (data: unknown) => ({ status: 'ready', data, queried_at: new Date().toISOString() })
const makeSnapshot = () => ({ scope: {}, from: '2026-10-27T01:23:00Z', until: '2026-10-28T01:23:00Z', usage: section({ summary: { requests: 1, samples: 1, unknown_samples: 0, p50_ms: null, p90_ms: null, p95_ms: null }, rows: [], total: 0, lineage: [], lineage_total: 0 }), quality: section({ summary: { normal: 0, degraded: 0, unknown: 0, failed: 0 }, rows: [], has_more: false }), errors: section({ records: 0, rows: [] }), capacity: section({ accounts: 1, schedulable: 1, configured_concurrency: 10 }) })
const mountView = () => mount(ChannelOperationsView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, RouterLink: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, ConfirmDialog: true } } })

describe('Channel operations', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(channels.list).mockResolvedValue({ items: [{ id: 1, name: 'Channel' }], total: 1 } as Awaited<ReturnType<typeof channels.list>>)
    vi.mocked(channels.getById).mockResolvedValue({ id: 1, name: 'Channel' } as Awaited<ReturnType<typeof channels.getById>>)
    vi.mocked(ops.snapshot).mockResolvedValue(makeSnapshot() as unknown as ops.Snapshot)
    vi.mocked(ops.catalog).mockResolvedValue({ revision: 3, upstream_total: 1, upstreams: [{ id: 9, name: 'Supplier', members: 1 }], total: 1, accounts: [{ id: 2, name: 'Account', platform: 'openai', status: 'active', schedulable: true, concurrency: 1, upstream_id: 9, eligible: true }] })
  })
  it('uses one exact time snapshot while paginating and keeps missing latency unknown', async () => {
    const wrapper = mountView(); await flushPromises()
    const query = vi.mocked(ops.snapshot).mock.calls[0][0]
    expect(Date.parse(query.until) - Date.parse(query.from)).toBe(86400_000)
    expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).not.toContain('0.00s')
    await (wrapper.vm as unknown as { refresh: () => Promise<void> }).refresh()
    expect(vi.mocked(ops.snapshot).mock.calls[1][0]).toMatchObject({ from: query.from, until: query.until })
    expect(ops.mutate).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('retains the channel while selecting a supplier and freezes revision for a change', async () => {
    const wrapper = mountView(); await flushPromises()
    const vm = wrapper.vm as unknown as { channelId: number; selectChannel: () => void; queue: (body: Omit<ops.Mutation, 'revision'>) => void; pending: { channelId: number; body: ops.Mutation }; confirmMutation: () => void }
    vm.channelId = 1; vm.selectChannel(); await flushPromises()
    expect(wrapper.text()).toContain('Supplier')
    vm.queue({ action: 'detach', account_id: 2, expected_upstream_id: 9, confirm: true })
    expect(vm.pending).toMatchObject({ channelId: 1, body: { revision: 3, expected_upstream_id: 9 } })
    vi.mocked(ops.mutate).mockRejectedValue({ status: 409 })
    vm.confirmMutation(); await flushPromises()
    expect(wrapper.text()).toContain('channelOps.conflict')
    expect(ops.mutate).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
