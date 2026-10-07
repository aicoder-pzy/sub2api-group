import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FastestFailoverSettings from '../FastestFailoverSettings.vue'
import { getFastestFailoverSettings, updateFastestFailoverSettings } from '@/api/admin/settings'

vi.mock('@/api/admin/settings', () => ({ getFastestFailoverSettings: vi.fn(), updateFastestFailoverSettings: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('global fastest failover settings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(getFastestFailoverSettings).mockResolvedValue({ first_output_timeout_seconds: 60, stream_idle_timeout_seconds: 120, model_cooldown_seconds: 120 })
    vi.mocked(updateFastestFailoverSettings).mockImplementation(async settings => ({ ...settings }))
  })
  it('loads, validates whole seconds and saves the full global configuration', async () => {
    const wrapper = mount(FastestFailoverSettings)
    await flushPromises()
    const first = wrapper.get('[data-testid="first_output_timeout_seconds"]')
    await first.setValue('1.5')
    await wrapper.get('button').trigger('click')
    expect(updateFastestFailoverSettings).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('invalid')
    await first.setValue('15')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(updateFastestFailoverSettings).toHaveBeenCalledWith({ first_output_timeout_seconds: 15, stream_idle_timeout_seconds: 120, model_cooldown_seconds: 120 })
    expect(wrapper.get('[role="status"]').text()).toContain('saved')
  })
  it('requires a successful load before allowing a save', async () => {
    vi.mocked(getFastestFailoverSettings).mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(FastestFailoverSettings)
    await flushPromises()
    expect(wrapper.find('fieldset').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('offline')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('fieldset').exists()).toBe(true)
  })
})
