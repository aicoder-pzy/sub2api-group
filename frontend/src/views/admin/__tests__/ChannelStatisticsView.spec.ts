import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelStatisticsView from '../ChannelStatisticsView.vue'
import messages from '@/i18n/locales/zh/channelStatistics'
import type { ChannelStatistics, ChannelStatisticsRow } from '@/api/admin/channelStatistics'

const api = vi.hoisted(() => ({ statistics: vi.fn(), groups: vi.fn() }))
vi.mock('@/api/admin/channelStatistics', () => ({ getChannelStatistics: api.statistics }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: api.groups }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({
  locale: { value: 'zh' },
  t: (key: string, params: Record<string, string | number> = {}) => {
    const message = messages[key.split('.').at(-1) as keyof typeof messages] ?? key
    return message.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? name))
  },
}) }))
const row = (patch: Partial<ChannelStatisticsRow> = {}): ChannelStatisticsRow => ({
  day: '', account_id: 1, account_name: 'A', account_deleted: false,
  requests: 20, successes: 18, failures: 2, unclassified_failures: 1,
  stream_requests: 20, nonstream_requests: 0, latency_samples: 20,
  ttft_average_ms: 1000, ttft_p50_ms: 1000, ttft_p95_ms: 2000,
  slow_30: 0, slow_60: 0, duration_p50_ms: 3000, duration_p95_ms: 4000,
  output_tps_p50: 50, context_tokens_p50: 1000, input_tokens: 100,
  output_tokens: 200, cache_creation_tokens: 0, cache_read_tokens: 300,
  standard_cost: 10, account_cost: 2, user_cost: 3, normal: 10, degraded: 0,
  unknown: 3, failed_tests: 1, ...patch,
})
const fixture = (): ChannelStatistics => ({
  start_date: '2026-10-12', end_date: '2026-10-12', timezone: 'Asia/Shanghai', generated_at: '2026-10-12T10:00:00Z',
  summary: row(), accounts: [row(), row({ account_id: 2, account_name: 'B', ttft_p50_ms: null, ttft_p95_ms: null, normal: 10, degraded: 1 })],
  daily: [row({ day: '2026-10-12' })], models: ['gpt-6.1-sol', 'gpt-6-astra'],
})
const mountView = () => mount(ChannelStatisticsView, {
  global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } },
})
let wrapper: ReturnType<typeof mountView>
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-12T16:30:00Z')) // Beijing Oct 13
  api.statistics.mockReset().mockResolvedValue(fixture())
  api.groups.mockReset().mockResolvedValue([{ id: 24, name: 'Codex Plus' }])
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('Channel statistics', () => {
  it('uses Beijing dates, rejects stale ranges, and queries exactly seven calendar days', async () => {
    wrapper = mountView()
    await flushPromises()
    expect(api.statistics.mock.calls[0]![0]).toMatchObject({ start_date: '2026-10-13', end_date: '2026-10-13', model: 'gpt-6.1-sol' })
    const dates = wrapper.findAll('input[type="date"]')
    expect(dates[0]!.attributes('min')).toBe('2026-10-07')
    await dates[0]!.setValue('2026-10-06')
    await wrapper.find('form').trigger('submit')
    expect(api.statistics).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[role="alert"]').text()).toContain('最近 7 天')
    await wrapper.findAll('button').find(b => b.text() === '近 7 天')!.trigger('click')
    await flushPromises()
    expect(api.statistics.mock.calls[1]![0]).toMatchObject({ start_date: '2026-10-07', end_date: '2026-10-13' })
  })

  it('keeps missing speed and insufficient intelligence distinct from zero and 100%', async () => {
    wrapper = mountView()
    await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    expect(rows[0]!.text()).toContain('90.00%')
    expect(rows[0]!.text()).toContain('$2.0000')
    expect(rows[0]!.findAll('td')[7]!.find('strong').text()).toBe('—')
    expect(rows[1]!.findAll('td')[3]!.find('strong').text()).toBe('— / —')
    expect(rows[1]!.findAll('td')[7]!.find('strong').text()).toBe('90.9%')
    await wrapper.findAll('select')[2]!.setValue('xhigh')
    await flushPromises()
    expect(api.statistics.mock.calls.at(-1)![0].effort).toBe('xhigh')
    expect(wrapper.text()).toContain('失败无法确认思考强度')
  })
})
