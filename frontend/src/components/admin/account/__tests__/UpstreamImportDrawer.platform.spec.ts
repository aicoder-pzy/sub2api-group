import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { expect, it } from 'vitest'
import UpstreamImportDrawer from '../UpstreamImportDrawer.vue'
import zh from '@/i18n/locales/zh/upstreamSites'
import type { UpstreamSite } from '@/api/admin/upstreamSites'

it('offers Grok as a local account platform', () => {
  const site: UpstreamSite = { id: 'site-1', name: 'Test', kind: 'newapi', base_url: 'https://example.invalid', inference_url: '', purchase_note: '', user_id: 1, proxy_id: null, revision: 1, account_count: 0 }
  const wrapper = mount(UpstreamImportDrawer, {
    props: { show: true, site, keys: [], groups: [], accounts: [] },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { upstreamSites: zh } } })],
      stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } }
    }
  })

  expect(wrapper.get('select[aria-label="本地账号平台"]').find('option[value="grok"]').text()).toBe('Grok')
  wrapper.unmount()
})
