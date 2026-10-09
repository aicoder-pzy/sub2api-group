import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({ generate: vi.fn(), exchange: vi.fn(), refresh: vi.fn(), update: vi.fn(), clear: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: {
  generateAuthUrl: mocks.generate, exchangeCode: mocks.exchange, refreshOpenAIToken: mocks.refresh,
  update: mocks.update, applyOAuthCredentials: mocks.update, clearError: mocks.clear
} } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
import ReAuthAccountModal from '../ReAuthAccountModal.vue'
import AdminReAuthAccountModal from '../../admin/account/ReAuthAccountModal.vue'

const Flow = defineComponent({
  props: { excelOauth: Boolean },
  data: () => ({ inputMethod: 'manual', authCode: 'code', oauthState: 'bps.test.PC' }),
  emits: ['generate-url', 'validate-refresh-token'],
  template: '<button @click="$emit(\'generate-url\')">generate</button>'
})

describe('Excel reauthorization', () => {
  it.each([true, false].flatMap(excel => [true, false].map(admin => ({ excel, admin }))))('retains the issuing client and account configuration; %s', async ({excel, admin}) => {
    const clientId = excel ? 'app_fnr0pYvVwwFDocDumLG3H2Bp' : 'app_EMoamEEZ73f0CkXaXp7hrann'
    mocks.generate.mockReset().mockResolvedValue({ auth_url: 'https://auth.openai.com/?state=bps.test.PC', session_id: 'session' })
    mocks.exchange.mockReset().mockResolvedValue({ access_token: 'new-at', refresh_token: 'new-rt', client_id: clientId })
    mocks.update.mockReset().mockResolvedValue({})
    mocks.clear.mockReset().mockResolvedValue({})
    const extra = { openai_bps_ticket: { bps: excel, models: ['gpt-6-astra'] }, max_sessions: 3 }
    const account = { id: 42, platform: 'openai', type: 'oauth', credentials: { client_id: clientId }, extra } as unknown as Account
    const wrapper = mount(admin ? AdminReAuthAccountModal : ReAuthAccountModal, {
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
    if (!admin) expect(mocks.clear).toHaveBeenCalledWith(42)
    wrapper.unmount()
  })

  it.each([true, false])('refreshes through the active admin entry with the issuing client; Excel=%s', async (excel) => {
    const clientId = excel ? 'app_fnr0pYvVwwFDocDumLG3H2Bp' : 'app_EMoamEEZ73f0CkXaXp7hrann'
    mocks.refresh.mockReset().mockResolvedValue({access_token: 'at', refresh_token: 'rt', client_id: clientId})
    mocks.update.mockReset().mockResolvedValue({id:42})
    const extra = {openai_bps_ticket: {bps:excel, models:['gpt-6-astra']}}
    const wrapper = mount(AdminReAuthAccountModal, {
      props: {show:true, account: {id:42, platform:'openai', type:'oauth', credentials:{client_id:clientId}, extra} as unknown as Account},
      global: {stubs: {BaseDialog:{template:'<div><slot /><slot name="footer" /></div>'}, OAuthAuthorizationFlow:Flow, Icon:true}}
    })
    wrapper.getComponent(Flow).vm.$emit('validate-refresh-token', 'paired-rt')
    await flushPromises()
    expect(mocks.refresh).toHaveBeenCalledWith('paired-rt', undefined, '/admin/openai/refresh-token', clientId)
    expect(mocks.update.mock.calls[0][1].extra).toEqual(extra)
    expect(mocks.update.mock.calls[0][1].credentials.client_id).toBe(clientId)
    wrapper.unmount()
  })
})
