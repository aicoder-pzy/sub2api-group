import { mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import type { UserMonitorView } from '@/api/channelMonitor'
import MonitorCardGrid from '../monitor/MonitorCardGrid.vue'
import MonitorAvailabilityRow from '../monitor/MonitorAvailabilityRow.vue'
import MonitorMetricPair from '../monitor/MonitorMetricPair.vue'
import MonitorTimeline from '../monitor/MonitorTimeline.vue'
import MonitorCard from '../monitor/MonitorCard.vue'
import MonitorIntelligenceSummary from '../monitor/MonitorIntelligenceSummary.vue'

vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorQuotaVisible: () => false }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))

it('shows one model panel per channel, preserving selection and independent results across refreshes', async () => {
  const item: UserMonitorView = {
    id: 14, name: 'Self-use', provider: 'openai', group_name: '', primary_model: 'gpt-6.2-astra',
    primary_status: 'operational', primary_latency_ms: 2350, primary_ping_latency_ms: 83,
    availability_7d: 98.5, has_availability_7d: true,
    timeline: [{ status: 'operational', latency_ms: 2350, ping_latency_ms: 83, checked_at: '2026-11-24T04:22:00Z' }],
    extra_models: [
      { model: 'gpt-6.2-sol', status: 'failed', latency_ms: null, ping_latency_ms: null, availability_7d: 82.5,
        intelligence: { state: 'failed', history: [] },
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
  expect(card.findAll('section[data-model]')).toHaveLength(1)
  const astraTab = card.get('[data-model-select="gpt-6.2-astra"]')
  const solTab = card.get('[data-model-select="gpt-6.2-sol"]')
  expect(astraTab.classes()).toContain('bg-emerald-100')
  expect(solTab.classes()).toContain('bg-red-100')
  expect(astraTab.attributes('aria-pressed')).toBe('true')
  const astra = card.get('[data-model="gpt-6.2-astra"]')
  expect(astra.findComponent(MonitorMetricPair).props('primaryValue')).toBe('2350')
  expect(astra.findComponent(MonitorTimeline).props('buckets')).toEqual(item.timeline)
  expect(astra.findComponent(MonitorAvailabilityRow).props('value')).toBe(98.5)
  expect(astra.find('monitor-intelligence-summary-stub').exists()).toBe(true)
  expect(astra.findComponent(MonitorIntelligenceSummary).props('model')).toBe(item.primary_model)
  expect(astra.findComponent(MonitorIntelligenceSummary).props('intelligence')).toEqual(item.intelligence)

  await solTab.trigger('click')
  expect(wrapper.emitted('cardClick')).toBeUndefined()
  expect(card.findAll('section[data-model]')).toHaveLength(1)
  expect(solTab.attributes('aria-pressed')).toBe('true')
  expect(astraTab.attributes('aria-pressed')).toBe('false')
  const sol = card.get('[data-model="gpt-6.2-sol"]')
  expect(sol.findComponent(MonitorMetricPair).props('primaryValue')).not.toBe('2350')
  expect(sol.findComponent(MonitorTimeline).props('buckets')).toEqual(item.extra_models[0]!.timeline)
  expect(sol.findComponent(MonitorAvailabilityRow).props('value')).toBe(82.5)
  expect(sol.find('monitor-intelligence-summary-stub').exists()).toBe(true)
  expect(sol.findComponent(MonitorIntelligenceSummary).props('model')).toBe('gpt-6.2-sol')
  expect(sol.findComponent(MonitorIntelligenceSummary).props('intelligence')).toEqual(item.extra_models[0]!.intelligence)
  await card.get('button[aria-label="gpt-6.2-sol (Self-use)"]').trigger('click')
  expect(wrapper.emitted('cardClick')?.[0]).toEqual([item, 'gpt-6.2-sol'])
  await wrapper.setProps({ window: '15d' })
  expect(sol.findComponent(MonitorAvailabilityRow).props('value')).toBe(77.5)
  await wrapper.setProps({ window: '30d' })
  expect(sol.findComponent(MonitorAvailabilityRow).props('value')).toBe(69.5)

  await wrapper.setProps({ items: [{ ...item, extra_models: item.extra_models.map(probe => ({ ...probe, latency_ms: 1234 })) }] })
  expect(solTab.attributes('aria-pressed')).toBe('true')
  expect(card.get('[data-model="gpt-6.2-sol"]').findComponent(MonitorMetricPair).props('primaryValue')).toBe('1234')
  await astraTab.trigger('click')
  expect(card.get('[data-model="gpt-6.2-astra"]').findComponent(MonitorAvailabilityRow).props('value')).toBe(91.5)
  await wrapper.setProps({ window: '15d' })
  expect(card.get('[data-model="gpt-6.2-astra"]').findComponent(MonitorAvailabilityRow).props('value')).toBe(94.5)
  await card.get('[data-model-select="untested"]').trigger('click')
  const untested = card.get('[data-model="untested"]')
  expect(untested.findComponent(MonitorTimeline).props('buckets')).toEqual([])
  expect(untested.findComponent(MonitorAvailabilityRow).props('value')).toBeNull()
  expect(untested.find('monitor-intelligence-summary-stub').exists()).toBe(false)
  await wrapper.setProps({ items: [{ ...item, extra_models: [] }] })
  expect(card.findAll('section[data-model]')).toHaveLength(1)
  expect(card.get('[data-model-select="gpt-6.2-astra"]').attributes('aria-pressed')).toBe('true')
  expect(card.get('[data-model="gpt-6.2-astra"]').find('monitor-intelligence-summary-stub').exists()).toBe(true)
  wrapper.unmount()
})

it('switches actual intelligence tests, answers and tooltip model without leaking a previous model dialog', async () => {
  const candyPoint = {
    status: 'normal' as const, checked_at: '2026-11-25T01:00:00Z', latency_ms: 1200,
    prompt: 'Fixed candy question', answer: '21', expected_answer: '21',
  }
  const item: UserMonitorView = {
    id: 14, name: 'Self-use', provider: 'openai', group_name: '', primary_model: 'gpt-6.2-sol',
    primary_status: 'operational', primary_latency_ms: 1000, primary_ping_latency_ms: null,
    availability_7d: 100, timeline: [],
    intelligence: { state: 'unknown', history: [], tests: [
      { id: 1, kind: 'candy', state: 'normal', history: [candyPoint] },
      { id: 2, kind: 'pelican', state: 'normal', history: [{ ...candyPoint, prompt: '', answer: '' }] },
    ] },
    extra_models: [
      { model: 'gpt-6.2-astra', status: 'operational', latency_ms: 2000,
        intelligence: { state: 'degraded', history: [], tests: [
          { id: 3, kind: 'candy', state: 'degraded', history: [
            { ...candyPoint, status: 'degraded', checked_at: '2026-11-25T01:05:00Z', answer: '29' },
          ] },
        ] } },
      { model: 'unconfigured-model', status: '', latency_ms: null,
        intelligence: { state: 'no_plan', history: [] } },
    ],
  }
  const wrapper = mount(MonitorCard, {
    attachTo: document.body,
    props: { item, window: '7d', availabilityValues: {}, countdownSeconds: 30 },
    global: { stubs: { Transition: true, ProviderIcon: true, MonitorMetricPair: true, MonitorAvailabilityRow: true, MonitorTimeline: true } },
  })
  expect(wrapper.findAll('[data-testid^="monitor-intelligence-test-"]')).toHaveLength(2)
  await wrapper.get('[data-testid="monitor-intelligence-test-1"] button').trigger('click')
  expect(document.body.querySelector('[data-testid="monitor-candy-output"]')?.textContent).toBe('21')
  await wrapper.get('[data-model-select="gpt-6.2-astra"]').trigger('click')
  expect(document.body.querySelector('[data-testid="monitor-intelligence-result"]')).toBeNull()
  expect(wrapper.findAll('[data-testid^="monitor-intelligence-test-"]')).toHaveLength(1)
  expect(wrapper.text()).not.toContain('pelicanTests.pelicanQuestion')
  const astraDot = wrapper.get('[data-testid="monitor-intelligence-test-3"] button')
  expect(astraDot.classes()).toContain('bg-amber-400')
  expect(astraDot.attributes('title')).toContain('gpt-6.2-astra')
  expect(astraDot.attributes('title')).not.toContain('gpt-6.2-sol')
  await astraDot.trigger('click')
  expect(document.body.querySelector('[data-testid="monitor-candy-output"]')?.textContent).toBe('29')
  expect(document.body.querySelector('[data-testid="monitor-intelligence-prompt"]')?.textContent).toBe(candyPoint.prompt)

  const refreshed = { ...item, extra_models: item.extra_models.map(probe => probe.model !== 'gpt-6.2-astra' ? probe : {
    ...probe, intelligence: { state: 'normal', history: [], tests: [
      { id: 3, kind: 'candy', state: 'normal', history: [{ ...candyPoint, checked_at: '2026-11-25T01:10:00Z' }] },
    ] },
  }) }
  await wrapper.setProps({ item: refreshed })
  expect(wrapper.findComponent(MonitorIntelligenceSummary).props('intelligence')).toEqual(refreshed.extra_models[0]!.intelligence)
  expect(wrapper.get('[data-model-select="gpt-6.2-astra"]').attributes('aria-pressed')).toBe('true')
  expect(document.body.querySelector('[data-testid="monitor-intelligence-result"]')).toBeNull()
  expect(wrapper.get('[data-testid="monitor-intelligence-test-3"] button').classes()).toContain('bg-emerald-500')
  await wrapper.get('[data-testid="monitor-intelligence-test-3"] button').trigger('click')
  expect(document.body.querySelector('[data-testid="monitor-candy-output"]')?.textContent).toBe('21')
  await wrapper.get('[data-model-select="unconfigured-model"]').trigger('click')
  expect(wrapper.text()).toContain('channelStatus.intelligence.no_plan')
  expect(wrapper.find('[data-testid="monitor-intelligence"] button').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('pelicanTests.pelicanQuestion')
  expect(document.body.querySelector('[data-testid="monitor-intelligence-result"]')).toBeNull()
  await wrapper.get('[data-model-select="gpt-6.2-sol"]').trigger('click')
  expect(wrapper.findAll('[data-testid^="monitor-intelligence-test-"]')).toHaveLength(2)
  expect(wrapper.get('[data-testid="monitor-intelligence-test-2"] button').attributes('title')).toContain('gpt-6.2-sol')
  wrapper.unmount()
})
