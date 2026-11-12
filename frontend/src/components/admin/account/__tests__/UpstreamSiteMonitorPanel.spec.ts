import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import UpstreamSiteMonitorPanel from '../UpstreamSiteMonitorPanel.vue'
import zh from '@/i18n/locales/zh/upstreamSites'
import { upstreamSitesAPI as api, type SiteMonitor, type SiteMonitorRow, type UpstreamSite } from '@/api/admin/upstreamSites'

vi.mock('@/api/admin/upstreamSites', () => ({ upstreamSitesAPI: { monitoring: vi.fn() } }))
const now = '2026-11-12T06:00:00Z'
const row = (overrides: Partial<SiteMonitorRow> = {}): SiteMonitorRow => ({ id: 'monitor-14', name: 'Channel', state: 'healthy', latency_kind: 'check', latency_ms: 1500, availability_7d: 99, cache_rate: 80, error_rate: null, checked_at: now, ...overrides })
const result = (rows = [row()]): SiteMonitor => ({ status: 'ok', mode: 'v1', attempt_at: now, received_at: now, data_at: now, refresh_seconds: 60, stale: false, rows })
const site: UpstreamSite = { id: 'site-1', name: 'Site', kind: 'sub2api', base_url: 'https://example.invalid', inference_url: '', purchase_note: '', user_id: 1, proxy_id: null, revision: 1, account_count: 0, inventory: { revision: 1, status: 'ok', attempt_at: now, errors: {}, groups: [{ id: '14', name: 'Current group' }], keys: [{ id: 'key', name: 'Key', group_id: '14', group_name: 'Current group', available: true, status: 'active' }] } }
let wrapper: VueWrapper
const view = () => mount(UpstreamSiteMonitorPanel, { props: { site }, global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { upstreamSites: zh } } })] } })
beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers()
  vi.setSystemTime(now)
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers(); vi.restoreAllMocks() })

it('matches only explicit groups and renders source text without executing it', async () => {
  vi.mocked(api.monitoring).mockResolvedValue(result([row({ id: '14', name: '<script>injected()</script>' }), row({ id: 'actual', group_id: '14', name: 'Matched' }), row({ id: 'named', group_name: 'Current group', name: 'Exact name' })]))
  wrapper = view(); await flushPromises()
  expect(wrapper.find('script').exists()).toBe(false)
  expect(wrapper.text()).toContain('<script>injected()</script>')
  expect(wrapper.get('[data-monitor-row="14"]').text()).not.toContain(zh.monitor.keyGroup)
  expect(wrapper.get('[data-monitor-row="actual"]').text()).toContain(zh.monitor.keyGroup)
  expect(wrapper.get('[data-monitor-row="named"]').text()).toContain(zh.monitor.keyGroup)
  await wrapper.get('select').setValue('keys')
  expect(wrapper.findAll('[data-monitor-row]')).toHaveLength(2)
  await wrapper.setProps({ site: { ...site, inventory: { ...site.inventory!, keys: [{ ...site.inventory!.keys[0], group_id: '999', group_name: 'Not monitored' }] } } })
  expect(wrapper.get('[data-monitor-unmatched]').text()).toContain('Not monitored')
  expect(wrapper.text()).toContain(zh.monitor.noMatched)
})

it('does not present old, undated or failed reads as current healthy status', async () => {
  vi.mocked(api.monitoring).mockResolvedValueOnce(result([row(), row({ id: 'old', checked_at: '2026-11-12T05:00:00Z' }), row({ id: 'undated', checked_at: undefined })])).mockRejectedValue(new Error('failed'))
  wrapper = view(); await flushPromises()
  expect(wrapper.get('[data-monitor-row="monitor-14"] .monitor-state').classes()).toContain('healthy')
  expect(wrapper.get('[data-monitor-row="old"] .monitor-state').text()).toBe(zh.monitor.stale)
  expect(wrapper.get('[data-monitor-row="undated"] .monitor-state').text()).toBe(zh.monitor.undated)
  await vi.advanceTimersByTimeAsync(60_000); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain(zh.monitor.retained)
  expect(wrapper.findAll('.monitor-state.healthy')).toHaveLength(0)
  expect(wrapper.findAll('[data-monitor-row]')).toHaveLength(3)
})

it('discards another site response and stops refreshing when hidden or unmounted', async () => {
  let resolveOld!: (value: SiteMonitor) => void
  vi.mocked(api.monitoring).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve })).mockResolvedValue(result([row({ name: 'New site' })]))
  wrapper = view(); await flushPromises()
  const oldSignal = vi.mocked(api.monitoring).mock.calls[0][1]!
  await wrapper.setProps({ site: { ...site, id: 'site-2' } }); await flushPromises()
  expect(oldSignal.aborted).toBe(true)
  resolveOld(result([row({ name: 'Old site' })])); await flushPromises()
  expect(wrapper.text()).toContain('New site')
  expect(wrapper.text()).not.toContain('Old site')
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
  document.dispatchEvent(new Event('visibilitychange'))
  await vi.advanceTimersByTimeAsync(120_000)
  expect(api.monitoring).toHaveBeenCalledTimes(2)
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
  expect(api.monitoring).toHaveBeenCalledTimes(3)
  wrapper.unmount()
  await vi.advanceTimersByTimeAsync(120_000)
  expect(api.monitoring).toHaveBeenCalledTimes(3)
})
