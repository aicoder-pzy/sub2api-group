import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({ generate: vi.fn(), exchange: vi.fn(), update: vi.fn(), clear: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: {
  generateAuthUrl: mocks.generate, exchangeCode: mocks.exchange, update: mocks.update, clearError: mocks.clear
} } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
import ReAuthAccountModal from '../ReAuthAccountModal.vue'

const Flow = defineComponent({
  props: { excelOauth: Boolean },
  data: () => ({ inputMethod: 'manual', authCode: 'code', oauthState: 'bps.test.PC' }),
  emits: ['generate-url'],
  template: '<button @click="$emit(\'generate-url\')">generate</button>'
})

describe('Excel reauthorization', () => {
  it.each([true, false])('retains the issuing client and account configuration; Excel=%s', async (excel) => {
    const clientId = excel ? 'app_fnr0pYvVwwFDocDumLG3H2Bp' : 'app_EMoamEEZ73f0CkXaXp7hrann'
    mocks.generate.mockReset().mockResolvedValue({ auth_url: 'https://auth.openai.com/?state=bps.test.PC', session_id: 'session' })
    mocks.exchange.mockReset().mockResolvedValue({ access_token: 'new-at', refresh_token: 'new-rt', client_id: clientId })
    mocks.update.mockReset().mockResolvedValue({})
    mocks.clear.mockReset().mockResolvedValue({})
    const extra = { openai_bps_ticket: { bps: excel, models: ['gpt-6-astra'] }, max_sessions: 3 }
    const account = { id: 42, platform: 'openai', type: 'oauth', credentials: { client_id: clientId }, extra } as unknown as Account
    const wrapper = mount(ReAuthAccountModal, {
      props: { show: true, account },
      global: { stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        OAuthAuthorizationFlow: Flow, ProxySelector: true, Select: true
      } }
    })
    expect(wrapper.getComponent(Flow).props('excelOauth')).toBe(excel)
    await wrapper.getComponent(Flow).trigger('click')
    await flushPromises()
    expect(mocks.generate).toHaveBeenLastCalledWith('/admin/openai/generate-auth-url', excel ? { oauth_client: 'excel' } : {})
    const submit = wrapper.findAll('button').find(button => button.text().includes('completeAuth'))!
    await submit.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(42, { type: 'oauth', credentials: { access_token: 'new-at', refresh_token: 'new-rt', client_id: clientId, expires_at: undefined }, extra })
    expect(mocks.clear).toHaveBeenCalledWith(42)
    wrapper.unmount()
  })
})
