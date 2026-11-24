import { mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import type { UserMonitorView } from '@/api/channelMonitor'
import MonitorCardGrid from '../monitor/MonitorCardGrid.vue'
import MonitorAvailabilityRow from '../monitor/MonitorAvailabilityRow.vue'
import MonitorMetricPair from '../monitor/MonitorMetricPair.vue'
import MonitorTimeline from '../monitor/MonitorTimeline.vue'

vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorQuotaVisible: () => false }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))

it('keeps each channel in one card with independent model results and window statistics', async () => {
  const item: UserMonitorView = {
    id: 14, name: 'Self-use', provider: 'openai', group_name: '', primary_model: 'gpt-6.2-astra',
    primary_status: 'operational', primary_latency_ms: 2350, primary_ping_latency_ms: 83,
    availability_7d: 98.5, has_availability_7d: true,
    timeline: [{ status: 'operational', latency_ms: 2350, ping_latency_ms: 83, checked_at: '2026-11-24T04:22:00Z' }],
    extra_models: [
      { model: 'gpt-6.2-sol', status: 'failed', latency_ms: null, ping_latency_ms: null, availability_7d: 82.5,
        timeline: [{ status: 'failed', latency_ms: null, ping_latency_ms: null, checked_at: '2026-11-24T04:22:00Z' }] },
      { model: 'untested', status: '', latency_ms: null, availability_7d: null, timeline: [] },
    ],
    intelligence: { state: 'normal', history: [] },
  }
  const wrapper = mount(MonitorCardGrid, {
    props: {
      items: [item, { ...item, id: 16, name: 'Other channel', extra_models: [] }],
      window: '7d', countdownSeconds: 30, loading: false,
      detailCache: {
        14: { id: 14, name: item.name, provider: item.provider, group_name: '', models: [
          { model: item.primary_model, latest_status: 'operational', latest_latency_ms: 2350,
            availability_7d: 98.5, availability_15d: 94.5, availability_30d: 91.5, avg_latency_7d_ms: 2350 },
          { model: 'gpt-6.2-sol', latest_status: 'failed', latest_latency_ms: null,
            availability_7d: 82.5, availability_15d: 77.5, availability_30d: 69.5, avg_latency_7d_ms: null },
        ] },
      },
    },
    global: { stubs: {
      ProviderIcon: true, MonitorMetricPair: true, MonitorAvailabilityRow: true,
      MonitorTimeline: true, MonitorIntelligenceSummary: true,
    } },
  })
  expect(wrapper.findAll('article')).toHaveLength(2)
  const card = wrapper.findAll('article')[0]!
  expect(card.findAll('h3')).toHaveLength(1)
  expect(card.get('h3').text()).toBe('Self-use')
  const astra = card.get('[data-model="gpt-6.2-astra"]')
  const sol = card.get('[data-model="gpt-6.2-sol"]')
  const untested = card.get('[data-model="untested"]')
  expect(astra.find('.bg-emerald-100').exists()).toBe(true)
  expect(sol.find('.bg-red-100').exists()).toBe(true)
  expect(astra.findComponent(MonitorMetricPair).props('primaryValue')).toBe('2350')
  expect(sol.findComponent(MonitorMetricPair).props('primaryValue')).not.toBe('2350')
  expect(astra.findComponent(MonitorTimeline).props('buckets')).toEqual(item.timeline)
  expect(sol.findComponent(MonitorTimeline).props('buckets')).toEqual(item.extra_models[0]!.timeline)
  expect(untested.findComponent(MonitorTimeline).props('buckets')).toEqual([])
  expect(astra.findComponent(MonitorAvailabilityRow).props('value')).toBe(98.5)
  expect(sol.findComponent(MonitorAvailabilityRow).props('value')).toBe(82.5)
  expect(untested.findComponent(MonitorAvailabilityRow).props('value')).toBeNull()
  expect(astra.find('monitor-intelligence-summary-stub').exists()).toBe(true)
  expect(sol.find('monitor-intelligence-summary-stub').exists()).toBe(false)
  await sol.get('button').trigger('click')
  expect(wrapper.emitted('cardClick')?.[0]).toEqual([item, 'gpt-6.2-sol'])
  await wrapper.setProps({ window: '15d' })
  expect(astra.findComponent(MonitorAvailabilityRow).props('value')).toBe(94.5)
  expect(sol.findComponent(MonitorAvailabilityRow).props('value')).toBe(77.5)
  await wrapper.setProps({ window: '30d' })
  expect(astra.findComponent(MonitorAvailabilityRow).props('value')).toBe(91.5)
  expect(sol.findComponent(MonitorAvailabilityRow).props('value')).toBe(69.5)
  expect(untested.findComponent(MonitorAvailabilityRow).props('value')).toBeNull()
  wrapper.unmount()
})
