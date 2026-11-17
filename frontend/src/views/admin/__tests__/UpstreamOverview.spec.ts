import { mount, flushPromises } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { expect, it, vi } from 'vitest'
import Balance from '@/components/admin/account/UpstreamBalanceOverview.vue'
import GroupSelect from '@/components/admin/account/UpstreamGroupSelect.vue'
import { upstreamSitesAPI as api, type UpstreamSite, type SiteMonitorRow } from '@/api/admin/upstreamSites'
import zh from '@/i18n/locales/zh/upstreamSites'

vi.mock('@/api/admin/upstreamSites', () => ({ upstreamSitesAPI: { monitoring: vi.fn() } }))
const global = () => ({ plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { upstreamSites: zh } } })] })
const site = (id: string, purpose: 'private' | 'public', currency: string, remaining: number, failed = false): UpstreamSite => ({
  id, name: id, purpose, kind: 'sub2api', base_url: 'https://test.invalid', inference_url: '', purchase_note: '', user_id: 1, proxy_id: null, revision: 1, account_count: 0,
  inventory: { revision: 1, status: 'ok', attempt_at: new Date().toISOString(), wallet_at: new Date().toISOString(), wallet: { currency, remaining, unlimited: false }, groups: [], keys: [], errors: failed ? { wallet: 'failed' } : {} }
})
it('separates purpose and currency, includes zero and excludes failed balances without treating unknown as zero', async () => {
  const unknown = site('unknown', 'private', 'USD', 500); delete unknown.inventory!.wallet
  const wrapper = mount(Balance, { global: global(), props: { sites: [site('one', 'private', 'USD', 12), site('zero', 'private', 'USD', 0), site('yuan', 'private', 'CNY', 100), site('failed', 'private', 'USD', 999, true), unknown, site('public', 'public', 'USD', 40)] } })
  await flushPromises()
  const total = wrapper.get('[data-balance-purpose="private"]').text()
  expect(total).toContain('USD 12.00'); expect(total).toContain('CNY 100.00')
  expect(total).toContain('3 / 5'); expect(total).toContain('2 个站点')
  expect(wrapper.get('[data-balance-purpose="public"]').text()).toContain('USD 40.00')
  await wrapper.get('select[aria-label="余额币种"]').setValue('USD')
  expect(wrapper.get('[data-balance-site="zero"]').text()).toContain('USD 0.00')
  expect(wrapper.get('[data-balance-site="unknown"]').text()).toContain('暂未读取')
  await wrapper.get('[data-balance-site="one"]').trigger('click')
  expect(wrapper.emitted('select')?.[0]).toEqual(['one'])
  wrapper.unmount()
})

it('shows each group status before selection, matches only exact group IDs or unique names, and marks stale data', async () => {
  const row = (id: string, group_id: string, state: SiteMonitorRow['state'], age = 0): SiteMonitorRow => ({ id, name: id, group_id, state, latency_kind: 'check', latency_ms: 200, availability_7d: 99, error_rate: null, cache_rate: null, checked_at: new Date(Date.now() - age).toISOString() })
  vi.mocked(api.monitoring).mockResolvedValue({ status: 'ok', attempt_at: new Date().toISOString(), refresh_seconds: 60, stale: false, rows: [row('good', '1', 'healthy'), row('bad', '2', 'critical'), row('old', '3', 'healthy', 16 * 60000), row('same name wrong ID', '999', 'healthy')] })
  const wrapper = mount(GroupSelect, { global: global(), props: { modelValue: '', groups: [{ id: '1', name: 'One', multiplier: 0 }, { id: '2', name: 'Two' }, { id: '3', name: 'Three' }, { id: '4', name: 'same name wrong ID' }], site: site('test', 'private', 'USD', 1) } })
  await flushPromises()
  expect(wrapper.get('[data-group-option="1"]').text()).toContain('正常')
  expect(wrapper.get('[data-group-option="1"]').text()).toContain('×0')
  expect(wrapper.get('[data-group-option="2"]').text()).toContain('异常')
  expect(wrapper.get('[data-group-option="3"]').text()).toContain('数据过期')
  expect(wrapper.get('[data-group-option="4"]').text()).toContain('未提供状态')
  expect(wrapper.find('[data-upstream-group-detail]').exists()).toBe(false)
  await wrapper.get('input[value="2"]').setValue(true)
  expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['2'])
  wrapper.unmount()
})
