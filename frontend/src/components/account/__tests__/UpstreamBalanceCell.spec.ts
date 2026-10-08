import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UpstreamBalanceCell from '../UpstreamBalanceCell.vue'
import type { Account, UpstreamBalanceState } from '@/types'
import { accountsAPI } from '@/api/admin/accounts'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { getBalance: vi.fn(), updateBalanceConfig: vi.fn(), probeBalance: vi.fn() } }))
const now = Date.parse('2026-10-09T12:00:00Z')
function mountCell(status: 'ok' | 'failed' = 'ok') {
  const state: UpstreamBalanceState = { enabled: true, provider: 'auto', currency: 'USD', quota_per_unit: 500000, pause_on_exhaustion: true,
    snapshot: { status, amounts: [{ scope: 'wallet', currency: 'USD', remaining: 0, unlimited: false }], received_at: '2026-10-09T11:50:00Z', fresh_until: '2026-10-09T12:50:00Z', last_attempt_at: '2026-10-09T11:50:00Z', next_probe_at: '2026-10-09T12:20:00Z' } }
  return mount(UpstreamBalanceCell, { props: { account: { id: 1, name: 'channel', type: 'apikey', extra: { upstream_balance_probe: state } } as Account, now, lowThreshold: 5 }, global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } } })
}
describe('UpstreamBalanceCell', () => {
  it('only marks a fresh successful zero as paused and retains failed data', () => {
    const success = mountCell()
    expect(success.text()).toContain('admin.accounts.balance.paused')
    success.unmount()
    const failed = mountCell('failed')
    expect(failed.text()).toContain('0 USD')
    expect(failed.text()).toContain('admin.accounts.balance.failed')
    expect(failed.text()).not.toContain('admin.accounts.balance.paused')
    expect(failed.text()).not.toContain('admin.accounts.balance.low')
    failed.unmount()
  })
  it('saves configuration without copying runtime snapshots', async () => {
    const wrapper = mountCell()
    vi.mocked(accountsAPI.updateBalanceConfig).mockResolvedValue({ enabled: true, provider: 'auto', currency: 'USD', quota_per_unit: 500000, pause_on_exhaustion: true })
    await wrapper.get('button[aria-label="admin.accounts.balance.config"]').trigger('click')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(accountsAPI.updateBalanceConfig).toHaveBeenCalledWith(1, { enabled: true, provider: 'auto', currency: 'USD', quota_per_unit: 500000, pause_on_exhaustion: true })
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })
})
