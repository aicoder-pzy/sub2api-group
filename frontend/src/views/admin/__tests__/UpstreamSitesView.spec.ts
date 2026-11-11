import { mount, flushPromises } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { beforeEach, expect, it, vi } from 'vitest'
import UpstreamSitesView from '../UpstreamSitesView.vue'
import zh from '@/i18n/locales/zh/upstreamSites'
import { upstreamSitesAPI as api, type UpstreamSite } from '@/api/admin/upstreamSites'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/api/admin/groups', () => ({ getAll: vi.fn().mockResolvedValue([]) }))
vi.mock('@/api/admin/proxies', () => ({ getAll: vi.fn().mockResolvedValue([]) }))
vi.mock('@/api/admin/upstreamSites', async () => ({ ...await vi.importActual('@/api/admin/upstreamSites'), upstreamSitesAPI: { list: vi.fn(), accounts: vi.fn(), sync: vi.fn(), modelPreview: vi.fn() } }))
const fixture = (): UpstreamSite => ({ id: 'site-1', name: 'Test upstream', kind: 'newapi', base_url: 'https://console.example.invalid', inference_url: 'https://relay.example.invalid', purchase_note: '', user_id: 7, proxy_id: null, revision: 1, account_count: 0, inventory: { status: 'attention', revision: 1, attempt_at: '2026-11-07T00:00:00Z', received_at: '2026-11-06T00:00:00Z', wallet_at: '2026-11-06T00:00:00Z', wallet: { currency: 'QUOTA', remaining: 23456, unlimited: false }, groups: [], keys: [{ id: '3', name: 'relay key', group_id: 'vip', group_name: 'VIP', available: true, status: 'active' }], errors: { wallet: 'Permission denied' } } })
const view = () => mount(UpstreamSitesView, { global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { upstreamSites: zh } } })], stubs: { RouterLink: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }, UpstreamImportDrawer: true } } })
beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.list).mockResolvedValue([fixture()]); vi.mocked(api.accounts).mockResolvedValue([]) })
it('distinguishes short-lived authorization from automatic renewal without returning credentials', async () => {
  const site = { ...fixture(), kind: 'sub2api' as const, can_refresh: false }
  vi.mocked(api.list).mockResolvedValue([site])
  const wrapper = view(); await flushPromises()
  await wrapper.findAll('[role=tab]')[3].trigger('click')
  expect(wrapper.get('[data-site-authorization]').text()).toBe(zh.manualRefresh)
  expect(wrapper.get<HTMLInputElement>('details input[readonly]').element.value).toContain("localStorage.getItem('refresh_token')")
  expect(wrapper.get<HTMLInputElement>('input[type=password]').element.value).toBe('')
  wrapper.unmount()
  vi.mocked(api.list).mockResolvedValue([{ ...site, can_refresh: true }])
  const configured = view(); await flushPromises()
  await configured.findAll('[role=tab]')[3].trigger('click')
  expect(configured.get('[data-site-authorization]').text()).toBe(zh.automaticRefresh)
  configured.unmount()
})
it('shows one wallet with its actual units and keeps the failed refresh visibly stale', async () => {
  const wrapper = view(); await flushPromises()
  expect(wrapper.findAll('[data-site-wallet]')).toHaveLength(1)
  expect(wrapper.get('[data-site-wallet]').text()).toBe('23,456 配额单位')
  expect(wrapper.text()).toContain('上次成功数据')
  expect(wrapper.text()).toContain('Permission denied')
  expect(wrapper.text()).not.toContain('¥')
  wrapper.unmount()
})
it('blocks importing when local associations could not be loaded', async () => {
  vi.mocked(api.accounts).mockRejectedValue(new Error('network'))
  const wrapper = view(); await flushPromises()
  const importButton = wrapper.findAll('button').find(button => button.text() === '导入')!
  expect(importButton.attributes('disabled')).toBeDefined()
  expect(wrapper.text()).toContain('关联账号未能加载')
  wrapper.unmount()
})
it('previews without applying and discards a previous preview before a failed refresh', async () => {
  vi.mocked(api.accounts).mockResolvedValue([{ id: 10, name: 'Local', platform: 'openai', status: 'active', source: 'imported', key_id: '3', models: { 'model-1': 'model-1' }, concurrency: 1, rate_multiplier: 1 }])
  vi.mocked(api.modelPreview).mockResolvedValue({ account_id: 10, version: 'snapshot', added: ['model-2'], removed: [], mapping: { 'model-1': 'model-1', 'model-2': 'model-2' } })
  const wrapper = view(); await flushPromises()
  await wrapper.findAll('[role=tab]')[2].trigger('click')
  await wrapper.findAll('button').find(button => button.text() === '预览模型变化')!.trigger('click'); await flushPromises()
  expect(vi.mocked(api.modelPreview).mock.calls[0]).toEqual(['site-1', 1, 10])
  expect(wrapper.text()).toContain('确认应用变化')
  vi.mocked(api.modelPreview).mockRejectedValue(new Error('catalog unavailable'))
  await wrapper.findAll('button').find(button => button.text() === '预览模型变化')!.trigger('click'); await flushPromises()
  expect(wrapper.text()).not.toContain('确认应用变化')
  expect(wrapper.text()).toContain('catalog unavailable')
  wrapper.unmount()
})
