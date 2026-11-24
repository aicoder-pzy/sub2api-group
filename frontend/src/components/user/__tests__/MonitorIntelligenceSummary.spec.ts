import { mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import type { MonitorIntelligence } from '@/api/channelMonitor'
import MonitorIntelligenceSummary from '../monitor/MonitorIntelligenceSummary.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))

it('shows the real latest and selected candy answers, never inventing an answer from the verdict', async () => {
  const intelligence: MonitorIntelligence = {
    state: 'unknown', history: [], tests: [
      { id: 1, kind: 'candy', state: 'normal', history: [
        { status: 'incorrect', checked_at: '2026-11-24T05:00:00Z', latency_ms: 1200, answer: '29', expected_answer: '21' },
        { status: 'normal', checked_at: '2026-11-24T05:15:00Z', latency_ms: 1300, answer: '最少取 **21 个**。\n原始说明 <script>alert(1)</script>', expected_answer: '21' },
      ] },
      { id: 2, kind: 'pelican', state: 'normal', history: [] },
      { id: 3, kind: 'custom', state: 'normal', history: [{ status: 'normal', checked_at: '2026-11-24T05:15:00Z', latency_ms: 1 }] },
    ],
  }
  const wrapper = mount(MonitorIntelligenceSummary, { props: { intelligence, model: 'gpt-6.2-sol' } })
  expect(wrapper.findAll('[data-testid="monitor-candy-answer"]')).toHaveLength(1)
  const candy = wrapper.get('[data-testid="monitor-intelligence-test-1"]')
  expect(candy.get('summary').text()).toContain('最少取 21 个。')
  expect(candy.get('summary').text()).toContain('channelStatus.intelligence.expectedAnswer：21')
  expect(candy.get('[data-testid="monitor-candy-output"]').text()).toContain('<script>alert(1)</script>')
  expect(wrapper.find('script').exists()).toBe(false)
  await candy.findAll('button')[0]!.trigger('click')
  expect(candy.get('summary').text()).toContain('channelStatus.intelligence.actualAnswer：29')
  expect(candy.get('[data-testid="monitor-candy-output"]').text()).toBe('29')
  await wrapper.setProps({ intelligence: { state: 'failed', history: [], tests: [
    { id: 1, kind: 'candy', state: 'failed', history: [{ status: 'failed', checked_at: '2026-11-24T05:30:00Z', latency_ms: 2000, expected_answer: '21' }] },
  ] } })
  expect(wrapper.get('summary').text()).toContain('channelStatus.intelligence.noAnswer')
  expect(wrapper.get('[data-testid="monitor-candy-output"]').text()).toBe('channelStatus.intelligence.noAnswer')
  await wrapper.setProps({ intelligence: { state: 'normal', history: [], tests: [
    { id: 1, kind: 'candy', state: 'normal', history: [{ status: 'normal', checked_at: '2026-11-24T05:45:00Z', latency_ms: 2000, answer: '21'.repeat(1000), expected_answer: '21', answer_truncated: true }] },
  ] } })
  expect(wrapper.text()).toContain('channelStatus.intelligence.answerTruncated')
  wrapper.unmount()
})
