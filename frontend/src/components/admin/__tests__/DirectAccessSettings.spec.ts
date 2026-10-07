import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DirectAccessSettings from '../DirectAccessSettings.vue'
import { getDirectAccessSettings, updateDirectAccessSettings } from '@/api/admin/settings'

vi.mock('@/api/admin/settings', () => ({
  getDirectAccessSettings: vi.fn(),
  updateDirectAccessSettings: vi.fn(),
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('DirectAccessSettings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(getDirectAccessSettings).mockResolvedValue({
      hostname: 'direct.easygpt.top', entries: [{ cidr: '47.239.86.227/32', note: 'existing' }],
    })
    vi.mocked(updateDirectAccessSettings).mockImplementation(async entries => ({ hostname: 'direct.easygpt.top', entries }))
  })

  it('loads the current allowlist and saves an additional CIDR with a note', async () => {
    const wrapper = mount(DirectAccessSettings)
    await flushPromises()
    expect(wrapper.text()).toContain('https://direct.easygpt.top/v1')
    expect(wrapper.get<HTMLInputElement>('[data-testid="direct-access-address"]').element.value).toBe('47.239.86.227/32')
    await wrapper.get('[data-testid="direct-access-add"]').trigger('click')
    await wrapper.findAll('[data-testid="direct-access-address"]')[1]!.setValue(' 203.0.113.0/24 ')
    await wrapper.findAll('[data-testid="direct-access-note"]')[1]!.setValue(' office ')
    await wrapper.get('[data-testid="direct-access-save"]').trigger('click')
    await flushPromises()
    expect(updateDirectAccessSettings).toHaveBeenCalledWith([
      { cidr: '47.239.86.227/32', note: 'existing' }, { cidr: '203.0.113.0/24', note: 'office' },
    ])
    expect(wrapper.get('[role="status"]').text()).toBe('admin.settings.directAccess.saved')
  })

  it('persists removal of the last entry and explains that empty means deny all', async () => {
    const wrapper = mount(DirectAccessSettings)
    await flushPromises()
    await wrapper.get('[data-testid="direct-access-remove"]').trigger('click')
    expect(wrapper.text()).toContain('admin.settings.directAccess.empty')
    await wrapper.get('[data-testid="direct-access-save"]').trigger('click')
    await flushPromises()
    expect(updateDirectAccessSettings).toHaveBeenCalledWith([])
  })

  it('rejects blank entries and preserves edits after a failed save', async () => {
    const wrapper = mount(DirectAccessSettings)
    await flushPromises()
    await wrapper.get('[data-testid="direct-access-add"]').trigger('click')
    await wrapper.get('[data-testid="direct-access-save"]').trigger('click')
    expect(updateDirectAccessSettings).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('addressRequired')
    await wrapper.findAll('[data-testid="direct-access-address"]')[1]!.setValue('bad')
    vi.mocked(updateDirectAccessSettings).mockRejectedValueOnce(new Error('invalid IP'))
    await wrapper.get('[data-testid="direct-access-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('invalid IP')
    expect(wrapper.findAll<HTMLInputElement>('[data-testid="direct-access-address"]')[1]!.element.value).toBe('bad')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('does not allow overwriting settings after a load failure', async () => {
    vi.mocked(getDirectAccessSettings).mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(DirectAccessSettings)
    await flushPromises()
    expect(wrapper.find('[data-testid="direct-access-save"]').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('offline')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="direct-access-save"]').exists()).toBe(true)
  })
})
