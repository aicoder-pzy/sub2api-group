import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BPSTicketsView from '../BPSTicketsView.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), saveSettings: vi.fn(), saveAccount: vi.fn(), probe: vi.fn(), harvest: vi.fn() }))
vi.mock('@/api/admin/bpsTickets', () => ({ bpsTicketsApi: api }))
vi.mock('@/api/admin/proxies', () => ({ getAll: vi.fn().mockResolvedValue([]) }))
vi.mock('@/api/admin/accounts', () => ({ getById: vi.fn() }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/account/AccountTestModal.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../settings/MihomoPoolSettings.vue', () => ({ default: { template: '<div />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' } }) }))

describe('BPS ticket management', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.get.mockResolvedValue({ settings: {}, limitation: '只能提供票据变化信号，不能证明模型降智。', accounts: [{ id: 7, name: 'OAuth account', status: 'active', schedulable: true, bps_eligible: true, config: { bps: false, models: ['gpt-6-astra'], proxy_source: 'account', auto_probe: true, auto_switch: true, tickets: true }, state: { models: { 'gpt-6-astra': { auto_bps: true } } }, tickets: [] }] })
    api.probe.mockResolvedValue({ verdict: 'inconclusive', model: 'gpt-6-astra', reason: '上游限流，无法判断', ticket_length: 0, continue_ticket_length: 0, latency_ms: 12, finished_at: '2026-10-07T00:00:00Z' })
  })
  it('a manual inconclusive probe displays the reason without changing routing', async () => {
    const wrapper = mount(BPSTicketsView)
    await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text() === '手动票据探针')!
    await button.trigger('click'); await flushPromises()
    expect(api.probe).toHaveBeenCalledWith(7, 'gpt-6-astra')
    expect(wrapper.text()).toContain('上游限流，无法判断')
    expect(wrapper.text()).toContain('自动 BPS 已生效')
    expect(api.saveAccount).not.toHaveBeenCalled()
    expect(api.saveSettings).not.toHaveBeenCalled()
    expect(api.harvest).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('manual BPS configuration preserves the selected model list', async () => {
    const wrapper = mount(BPSTicketsView)
    await flushPromises()
    await wrapper.get('textarea').setValue('gpt-6-astra\ngpt-5.6-sol')
    await wrapper.findAll('input[type="checkbox"]')[0]!.setValue(true)
    await wrapper.findAll('button').find(b => b.text() === '保存账号设置')!.trigger('click')
    await flushPromises()
    expect(api.saveAccount).toHaveBeenCalledWith(7, expect.objectContaining({ bps: true, models: ['gpt-6-astra', 'gpt-5.6-sol'], auto_switch: true }))
    wrapper.unmount()
  })
})
