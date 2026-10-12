import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BalanceNotificationSettings from '../BalanceNotificationSettings.vue'
import { getBalanceNotificationHistory, getSchedulingNotificationHistory, getBalanceNotificationSettings, saveBalanceNotificationSettings } from '@/api/admin/accounts'

vi.mock('@/api/admin/accounts', () => ({ getBalanceNotificationHistory: vi.fn(), getSchedulingNotificationHistory: vi.fn(), getBalanceNotificationSettings: vi.fn(), saveBalanceNotificationSettings: vi.fn(), testBalanceNotification: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('administrator switch notifications', () => {
  it('saves the independent switch preference and shows delivery history', async () => {
    const config = { enabled: false, notify_recovery: true, notify_account_switch: false, encryption_key_configured: true, channels: [] }
    vi.mocked(getBalanceNotificationSettings).mockResolvedValue(config)
    vi.mocked(saveBalanceNotificationSettings).mockImplementation(async value => ({ ...value }))
    vi.mocked(getBalanceNotificationHistory).mockResolvedValue([])
    vi.mocked(getSchedulingNotificationHistory).mockResolvedValue([{ id: 'event-1', group_id: 24, group_name: 'Codex', model: 'sol', previous_account_id: 118, previous_account_name: 'vulcan', account_id: 113, account_name: 'binzzz', trigger: 'HTTP 500', changed_at: '2026-10-12T04:31:00Z', status: 'sent', deliveries: {} }])
    const wrapper = mount(BalanceNotificationSettings)
    await flushPromises()
    expect(wrapper.text()).toContain('vulcan (#118) → binzzz (#113)')
    expect(wrapper.text()).toContain('HTTP 500')
    await wrapper.findAll('input[type="checkbox"]')[2]!.setValue(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveBalanceNotificationSettings).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, notify_account_switch: true }))
  })
})
