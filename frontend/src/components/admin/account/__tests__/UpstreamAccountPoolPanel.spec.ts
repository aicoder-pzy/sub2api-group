import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import UpstreamAccountPoolPanel from '../UpstreamAccountPoolPanel.vue'
import en from '@/i18n/locales/en'
import { upstreamSitesAPI as api, type SitePoolDisplay } from '@/api/admin/upstreamSites'

vi.mock('@/api/admin/upstreamSites', () => ({
  upstreamSitesAPI: { accountPool: vi.fn(), refreshAccountPool: vi.fn(), renameAccountPool: vi.fn() },
  siteError: (err: Error) => err.message
}))

it('shows native OAuth rows and filters by upstream membership even when display names match', async () => {
  const snapshot: SitePoolDisplay = {
    updated: '2026-11-19T09:13:00Z',
    pools: [
      { id: 'pool-id', name: 'Shared alias', alias: 'Shared alias', original: 'Upstream A', count: 31 },
      { id: 'other-id', name: 'Shared alias', alias: 'Shared alias', original: 'Upstream B', count: 2 },
    ],
    rows: Array.from({ length: 32 }, (_, i) => ({
      number: i + 1, platform: i === 31 ? 'grok' : 'openai', plan: 'Pro', status: 'active',
      pools: i < 30 ? ['pool-id'] : i === 30 ? ['pool-id', 'other-id'] : ['other-id'],
      current: 0, limit: 2, windows: [{ name: '5h', used: 25 }]
    }))
  }
  vi.mocked(api.accountPool).mockResolvedValueOnce(snapshot)
  vi.mocked(api.refreshAccountPool).mockRejectedValueOnce(new Error('Refresh failed'))
  vi.mocked(api.renameAccountPool).mockResolvedValueOnce({
    ...snapshot, pools: [{ ...snapshot.pools[0]!, name: 'My pool', alias: 'My pool' }, snapshot.pools[1]!]
  })
  const wrapper = mount(UpstreamAccountPoolPanel, {
    props: { siteId: 'site-id' },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })] }
  })
  try {
    await flushPromises()
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
    expect(wrapper.get('tbody tr').text()).toMatch(/OpenAI.*OAuth.*Pro.*25%/s)
    expect(wrapper.get('tbody tr').text()).not.toContain('Pro 200')
    expect(wrapper.get('tbody tr .badge-success').text()).toBe('Available')
    expect(wrapper.find('tbody button').exists()).toBe(false)
    await wrapper.get('[aria-label="Next page"]').trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    const group = wrapper.get('[aria-label="Upstream group"]')
    expect(group.text()).toContain('Upstream B · Shared alias (2)')
    await group.setValue('other-id')
    expect(wrapper.findAll('tbody tr').map(row => row.get('td').text())).toEqual(['#31', '#32'])
    await wrapper.get('[aria-label="Platform"]').setValue('grok')
    expect(wrapper.findAll('tbody tr').map(row => row.get('td').text())).toEqual(['#32'])
    await wrapper.get('[aria-label="Platform"]').setValue('')
    await group.setValue('pool-id')
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
    await wrapper.get('input').setValue('My pool')
    await wrapper.get('input').trigger('keydown.enter')
    await flushPromises()
    expect(api.renameAccountPool).toHaveBeenCalledWith('site-id', 'pool-id', 'My pool')
    expect(wrapper.get('option[value="pool-id"]').text()).toContain('Upstream A · My pool')
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
    await wrapper.findAll('button').find(b => b.text() === 'Refresh pools')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Refresh failed')
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
  } finally {
    wrapper.unmount()
  }
})
