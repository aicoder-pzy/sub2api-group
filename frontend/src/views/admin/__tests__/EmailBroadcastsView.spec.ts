import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import EmailBroadcastsView from '../EmailBroadcastsView.vue'
import * as api from '@/api/admin/emailBroadcasts'
import promotionTemplate from '@/assets/templates/91token-promotion.html?raw'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/api/admin/emailBroadcasts', () => ({ preview: vi.fn(), create: vi.fn(), list: vi.fn(), detail: vi.fn(), test: vi.fn(), cancel: vi.fn() }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
const mountView = () => mount(EmailBroadcastsView, { global: { stubs: { RouterLink: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } } })
type VM = { recipients: string; subject: string; content: string; contentFormat: api.BroadcastContentFormat; unsubscribeUrl: string; preview: api.RecipientPreview | null; pending: api.BroadcastInput | null; previewRecipients: () => Promise<void>; prepareSend: () => Promise<void>; submit: () => Promise<void>; useValidRecipients: () => void; loadPromotionTemplate: () => void; resolvedContent: () => string; previewHtml: (source: string, url?: string) => string }

describe('Email broadcasts', () => {
  const wrappers: ReturnType<typeof mountView>[] = []
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(api.list).mockResolvedValue({ items: [] })
    vi.mocked(api.preview).mockResolvedValue({ recipients: ['123456789@qq.com'], invalid: [], duplicates: 1 })
  })
  afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })
  const view = () => { const wrapper = mountView(); wrappers.push(wrapper); return { wrapper, vm: wrapper.vm as unknown as VM } }

  it('requires review, blocks invalid input, and reuses the request ID after an ambiguous response', async () => {
    const { wrapper, vm } = view(); await flushPromises()
    vm.recipients = '123456789,123456789@qq.com'; vm.subject = 'Notice'; vm.content = '<b>plain text</b>'; vm.contentFormat = 'text'
    await vm.prepareSend()
    expect(api.create).not.toHaveBeenCalled()
    expect(wrapper.html()).not.toContain('<b>plain text</b>')
    expect(vm.pending?.recipients).toBe('123456789@qq.com')
    const id = vm.pending?.id
    vi.mocked(api.create).mockRejectedValue({ message: 'Connection lost' })
    await vm.submit()
    expect(vm.pending?.id).toBe(id)
    await vm.prepareSend(); await vm.submit()
    expect(api.create).toHaveBeenCalledTimes(2)
    expect(vi.mocked(api.create).mock.calls[1][0].id).toBe(id)
    vm.recipients = 'invalid'
    vi.mocked(api.preview).mockResolvedValue({ recipients: [], invalid: ['invalid'], duplicates: 0 })
    vm.pending = null; await vm.prepareSend()
    expect(vm.pending).toBeNull()
    expect(wrapper.text()).toContain('emailBroadcast.invalidHint')
  })
  it('ignores a stale preview and makes dropping invalid entries explicit', async () => {
    const { vm } = view(); await flushPromises()
    let resolve!: (value: api.RecipientPreview) => void
    vi.mocked(api.preview).mockReturnValueOnce(new Promise(r => { resolve = r }))
    vm.recipients = '12345'
    const pending = vm.previewRecipients()
    vm.recipients = '67890'
    resolve({ recipients: ['12345@qq.com'], invalid: [], duplicates: 0 })
    await pending
    expect(vm.preview).toBeNull()
    vi.mocked(api.preview).mockResolvedValue({ recipients: ['67890@qq.com'], invalid: ['bad'], duplicates: 0 })
    await vm.previewRecipients()
    expect(vm.recipients).toBe('67890')
    vm.useValidRecipients()
    expect(vm.recipients).toBe('67890@qq.com')
    expect(vm.preview?.invalid).toEqual([])
    expect(api.create).not.toHaveBeenCalled()
  })

  it('shows the promotion and allows sending without an unsubscribe URL', async () => {
    const { wrapper, vm } = view(); await flushPromises()
    expect(vm.contentFormat).toBe('html')
    expect(vm.subject).toBe('emailBroadcast.promotionSubject')
    expect(vm.content).toBe(promotionTemplate)
    expect(vm.content).toContain('福利-全模型不降智')
    expect(vm.content).toContain('分组倍率 0.09×')
    expect(wrapper.find('iframe[sandbox=""]').exists()).toBe(true)
    expect(wrapper.find('iframe').attributes('srcdoc')).toBe(vm.previewHtml(promotionTemplate))
    expect(() => vm.resolvedContent()).not.toThrow()
    expect(vm.previewHtml(vm.content)).not.toContain('取消订阅')
    expect(vm.previewHtml(vm.content)).not.toContain('{{unsubscribe_url}}')
    expect(vm.previewHtml(vm.content)).toContain('91tokenai.top/sign-up')
    vm.recipients = '123456789'
    await vm.prepareSend()
    expect(vm.pending?.content_format).toBe('html')
    expect(vm.pending?.unsubscribe_url).toBe('')
    expect(vm.previewHtml(vm.pending!.content, vm.pending!.unsubscribe_url)).not.toContain('取消订阅')
    expect(api.create).not.toHaveBeenCalled()
    vm.unsubscribeUrl = 'http://example.com/unsubscribe'
    expect(() => vm.resolvedContent()).toThrow('unsubscribeUrlRequired')
    vm.unsubscribeUrl = 'https://example.com/unsubscribe?x=1&y=2'
    expect(() => vm.resolvedContent()).not.toThrow()
    expect(vm.previewHtml(vm.content, vm.unsubscribeUrl)).toContain('https://example.com/unsubscribe?x=1&amp;y=2')
    await vm.prepareSend()
    expect(vm.pending?.content).toContain('{{unsubscribe_url}}')
    expect(vm.pending?.unsubscribe_url).toBe(vm.unsubscribeUrl)
  })

  it('keeps the built-in template accessible from plain text without replacing a custom subject', async () => {
    const { wrapper, vm } = view(); await flushPromises()
    vm.contentFormat = 'text'; vm.content = 'Custom message'; vm.subject = 'Custom subject'
    await nextTick()
    await wrapper.findAll('button').find(button => button.text() === 'emailBroadcast.loadTemplate')!.trigger('click')
    expect(vm.contentFormat).toBe('html')
    expect(vm.content).toBe(promotionTemplate)
    expect(vm.subject).toBe('Custom subject')
    expect(wrapper.find('iframe[sandbox=""]').exists()).toBe(true)
  })
})
