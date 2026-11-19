import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import UpstreamAccountPoolPanel from '../UpstreamAccountPoolPanel.vue'
import en from '@/i18n/locales/en/upstreamSites'
import { upstreamSitesAPI as api, type SitePoolDisplay } from '@/api/admin/upstreamSites'

vi.mock('@/api/admin/upstreamSites', () => ({
  upstreamSitesAPI: { accountPool: vi.fn(), refreshAccountPool: vi.fn(), renameAccountPool: vi.fn() },
  siteError: (err: Error) => err.message
}))

it('filters and pages the snapshot, saves a display name and retains data on failed refresh', async () => {
  const snapshot: SitePoolDisplay = {
    updated: '2026-11-19T09:13:00Z',
    pools: [{ id: 'pool-id', name: 'Pool A', original: 'Admin-only name', count: 31 }],
    rows: Array.from({ length: 32 }, (_, i) => ({
      number: i + 1, platform: i === 31 ? 'grok' : 'openai', plan: 'Pro', status: 'active',
      pools: i === 31 ? [] : ['pool-id'], windows: [{ name: '5h', used: 25 }]
    }))
  }
  vi.mocked(api.accountPool).mockResolvedValueOnce(snapshot)
  vi.mocked(api.refreshAccountPool).mockRejectedValueOnce(new Error('Refresh failed'))
  vi.mocked(api.renameAccountPool).mockResolvedValueOnce({
    ...snapshot, pools: [{ ...snapshot.pools[0]!, name: 'My pool', alias: 'My pool' }]
  })
  const wrapper = mount(UpstreamAccountPoolPanel, {
    props: { siteId: 'site-id' },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { upstreamSites: en } } })] }
  })
  try {
    await flushPromises()
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
    expect(wrapper.get('tbody tr').text()).toContain('25%')
    await wrapper.get('[aria-label="Next page"]').trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    await wrapper.get('[aria-label="Account pools"]').setValue('pool-id')
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
    await wrapper.get('input').setValue('My pool')
    await wrapper.get('input').trigger('keydown.enter')
    await flushPromises()
    expect(api.renameAccountPool).toHaveBeenCalledWith('site-id', 'pool-id', 'My pool')
    expect(wrapper.get('option[value="pool-id"]').text()).toContain('My pool')
    await wrapper.findAll('button').find(b => b.text() === 'Refresh pools')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Refresh failed')
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
  } finally {
    wrapper.unmount()
  }
})
