import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import UpstreamAccountPoolPanel from '../UpstreamAccountPoolPanel.vue'
import en from '@/i18n/locales/en'
import { upstreamSitesAPI as api, type SitePoolDisplay } from '@/api/admin/upstreamSites'

vi.mock('@/api/admin/upstreamSites', () => ({
  upstreamSitesAPI: { accountPool: vi.fn(), groupAccountPool: vi.fn(), refreshAccountPool: vi.fn(), renameAccountPool: vi.fn() },
  siteError: (err: Error) => err.message
}))

it('shows native OAuth rows and filters by upstream membership even when display names match', async () => {
  const snapshot: SitePoolDisplay = {
    updated: '2026-11-19T09:13:00Z',
    checked_at: new Date().toISOString(),
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
  const info = { total: 32, pages: 2, platforms: ['grok', 'openai'], plans_by_pool: {} }
  const renamed = { ...snapshot, pools: [{ ...snapshot.pools[0]!, name: 'My pool', alias: 'My pool' }, snapshot.pools[1]!] }
  vi.mocked(api.accountPool)
    .mockResolvedValueOnce({ ...snapshot, page_info: info, rows: snapshot.rows.slice(0, 30) })
    .mockResolvedValueOnce({ ...snapshot, page_info: info, rows: snapshot.rows.slice(30) })
    .mockResolvedValueOnce({ ...snapshot, page_info: { ...info, pages: 1 }, rows: snapshot.rows.slice(30) })
    .mockResolvedValueOnce({ ...snapshot, page_info: { ...info, pages: 1 }, rows: snapshot.rows.slice(31) })
    .mockResolvedValueOnce({ ...snapshot, page_info: { ...info, pages: 1 }, rows: snapshot.rows.slice(30) })
    .mockResolvedValueOnce({ ...snapshot, page_info: info, rows: snapshot.rows.slice(0, 30) })
    .mockResolvedValueOnce({ ...renamed, page_info: info, rows: snapshot.rows.slice(0, 30) })
  vi.mocked(api.refreshAccountPool).mockRejectedValueOnce(new Error('Refresh failed'))
  vi.mocked(api.renameAccountPool).mockResolvedValueOnce(renamed)
  const wrapper = mount(UpstreamAccountPoolPanel, {
    props: { siteId: 'site-id' },
    global: { stubs: { RouterLink: RouterLinkStub }, plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })] }
  })
  try {
    await flushPromises()
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
    expect(wrapper.get('tbody tr').text()).toMatch(/OpenAI.*OAuth.*Pro.*25%/s)
    expect(wrapper.get('tbody tr').text()).not.toContain('Pro 200')
    expect(wrapper.get('tbody tr .badge-success').text()).toBe('Available')
    expect(wrapper.find('tbody button').exists()).toBe(false)
    await wrapper.get('[aria-label="Next page"]').trigger('click')
    await flushPromises()
    expect(api.accountPool).toHaveBeenLastCalledWith('site-id', expect.objectContaining({ page: 2, page_size: 30 }))
    expect(wrapper.findAll('tbody tr')).toHaveLength(2)
    const group = wrapper.get('[aria-label="Upstream group"]')
    expect(group.text()).toContain('Upstream B · Shared alias (2)')
    await group.setValue('other-id')
    await flushPromises()
    expect(api.accountPool).toHaveBeenLastCalledWith('site-id', expect.objectContaining({ page: 1, pool_id: 'other-id' }))
    expect(wrapper.findAll('tbody tr').map(row => row.get('td').text())).toEqual(['#31', '#32'])
    await wrapper.get('[aria-label="Platform"]').setValue('grok')
    await flushPromises()
    expect(api.accountPool).toHaveBeenLastCalledWith('site-id', expect.objectContaining({ platform: 'grok' }))
    expect(wrapper.findAll('tbody tr').map(row => row.get('td').text())).toEqual(['#32'])
    await wrapper.get('[aria-label="Platform"]').setValue('')
    await flushPromises()
    await group.setValue('pool-id')
    await flushPromises()
    expect(wrapper.findAll('tbody tr')).toHaveLength(30)
    await wrapper.get('input').setValue('My pool')
    await wrapper.get('input').trigger('keydown.enter')
    await flushPromises()
    expect(api.renameAccountPool).toHaveBeenCalledWith('site-id', 'pool-id', 'My pool', expect.objectContaining({ page: 1 }))
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

it('shows snapshot rows and allows paging while a full collection is running', async () => {
  const snapshot: SitePoolDisplay = {
    updated: '2026-11-19T09:13:00Z', pools: [], refreshing: true,
    page_info: { total: 32, pages: 2, platforms: ['openai'], plans_by_pool: {} },
    rows: [32, 30, 31, 1].map(number => ({ number, platform: 'openai', plan: 'Pro Max', status: 'active' })),
  }
  snapshot.rows[0] = { ...snapshot.rows[0]!, last_used: '2026-11-19T09:12:00Z' }
  snapshot.rows[2] = { ...snapshot.rows[2]!, admin: { usage_updated: '2026-11-19T09:11:00Z' } }
  snapshot.rows[1] = { ...snapshot.rows[1]!, current: 2, limit: 4, last_used: '2026-11-19T09:00:00Z',
    admin: { observed_at: '2026-11-19T09:11:30Z', account_rate: 0.12 } }
  snapshot.rows[3] = { ...snapshot.rows[3]!, current: 1, limit: 4 }
  vi.mocked(api.accountPool).mockResolvedValueOnce(snapshot).mockResolvedValueOnce({ ...snapshot, rows: [{ ...snapshot.rows[0]!, number: 29 }] })
  vi.mocked(api.refreshAccountPool).mockClear()
  const wrapper = mount(UpstreamAccountPoolPanel, {
    props: { siteId: 'old-site' },
    global: { stubs: { RouterLink: RouterLinkStub }, plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })] },
  })
  try {
    await flushPromises()
    expect(api.refreshAccountPool).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Recent activity')
    expect(wrapper.text()).toContain('Showing a previous snapshot')
    const rows = wrapper.findAll('tbody tr')
    expect(rows.slice(0, 4).map(row => row.get('td').text())).toEqual(['#32', '#30', '#31', '#1'])
    expect(rows[0]!.findAll('td').at(-1)!.get('[title]').attributes('title')).toMatch(/^Last used/)
    expect(rows[1]!.findAll('td').at(-1)!.get('[title]').attributes('title')).toMatch(/^Concurrency observed.*Last used/)
    expect(rows[1]!.text()).toContain('0.12x')
    expect(rows[2]!.findAll('td').at(-1)!.get('[title]').attributes('title')).toMatch(/^Quota updated/)
    expect(rows[3]!.findAll('td').at(-1)!.text()).toBe('—')
    await wrapper.get('[aria-label="Next page"]').trigger('click')
    await flushPromises()
    expect(api.accountPool).toHaveBeenLastCalledWith('old-site', expect.objectContaining({ page: 2 }))
    expect(wrapper.get('tbody tr').text()).toContain('#29')
  } finally {
    wrapper.unmount()
  }
})

it('previews the same paginated group pool without offering single-site controls', async () => {
  const page = {
    group_id: 47, name: 'ultra', updated: '2026-11-19T09:13:00Z', verified_at: null,
    complete: false, total: 31, plans: [{ code: 'pro_500', name: 'Pro 500', count: 31 }], pages: 2,
    rows: [{ number: 1, platform: 'openai', plan: 'Pro 500', status: 'active' }],
  }
  vi.mocked(api.groupAccountPool).mockResolvedValueOnce(page).mockResolvedValueOnce({ ...page, rows: [{ ...page.rows[0]!, number: 31 }] })
  const wrapper = mount(UpstreamAccountPoolPanel, {
    props: { groupId: 47 },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })] },
  })
  try {
    await flushPromises()
    expect(api.groupAccountPool).toHaveBeenCalledWith(47, 1, 30)
    expect(wrapper.text()).toContain('31 accounts')
    expect(wrapper.text()).toContain('Pool data is incomplete')
    expect(wrapper.get('tbody tr').text()).toMatch(/OpenAI.*OAuth.*Pro 500/s)
    expect(wrapper.find('[aria-label="Upstream group"]').exists()).toBe(false)
    await wrapper.get('[aria-label="Next page"]').trigger('click')
    await flushPromises()
    expect(api.groupAccountPool).toHaveBeenCalledWith(47, 2, 30)
    expect(wrapper.get('tbody tr').text()).toContain('#31')
  } finally {
    wrapper.unmount()
  }
})
