import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import en from '@/i18n/locales/en'
import { upstreamSitesAPI as api } from '@/api/admin/upstreamSites'
import UpstreamPoolStatusForm from '../UpstreamPoolStatusForm.vue'

vi.mock('@/api/admin/upstreamSites', () => ({
  upstreamSitesAPI: { poolStatusConfig: vi.fn(), savePoolStatusConfig: vi.fn() },
  siteError: (error: Error) => error.message,
}))

it('publishes the displayed plans after a source plan is renamed', async () => {
  const blank = { pool_id: '', source_name: '', name: '', plan_map: null, plans: null }
  vi.mocked(api.poolStatusConfig).mockResolvedValue(blank)
  vi.mocked(api.savePoolStatusConfig).mockImplementation(async (_, cfg) => cfg)
  const wrapper = mount(UpstreamPoolStatusForm, {
    props: { siteId: 'qiu', snapshot: { updated: null,
      pools: [{ id: 'pool', name: 'Default', original: 'Original pool', count: 2 }],
      rows: ['Pro 500', 'Team'].map((plan, index) => ({ number: index + 1, platform: 'openai', plan, status: 'active', pools: ['pool'] })),
    } },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })] },
  })
  await flushPromises()
  await wrapper.get('select').setValue('pool')
  const [source, name] = wrapper.findAll('input[required]')
  await source!.setValue('Qiu API')
  await name!.setValue('Codex pool')
  await wrapper.get('[aria-label="Public name for Pro 500"]').setValue('Pro Max')
  await wrapper.get('input[type="checkbox"][value="Pro Max"]').setValue(true)
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(api.savePoolStatusConfig).toHaveBeenCalledWith('qiu', {
    pool_id: 'pool', source_name: 'Qiu API', name: 'Codex pool', plan_map: { 'pro 500': 'Pro Max' }, plans: ['Pro Max'],
  })
  expect(wrapper.text()).toContain('Status API settings saved')
  wrapper.unmount()
})
