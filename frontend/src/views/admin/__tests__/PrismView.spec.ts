import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PrismView from '../PrismView.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), health: vi.fn(), test: vi.fn() }))
vi.mock('@/api/admin/prism', () => ({ prismApi: api }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' } }) }))

describe('Prism administrator tests', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.get.mockResolvedValue({ settings: { enabled: true, base_url: 'http://127.0.0.1:8319/v1', api_key_configured: true }, models: ['gpt-5.6-sol', 'gpt-6-luna'], efforts: ['medium', 'high'], accounts: [{ id: 7, name: 'OAuth test', status: 'active' }] })
    api.save.mockResolvedValue({ enabled: true, base_url: 'http://127.0.0.1:8319/v1', api_key_configured: true })
  })
  it('keeps secrets empty and clears newly saved keys', async () => {
    const wrapper = mount(PrismView)
    await flushPromises()
    expect((wrapper.get('[data-testid="bridge-key"]').element as HTMLInputElement).value).toBe('')
    await wrapper.get('[data-testid="bridge-key"]').setValue('k'.repeat(32))
    await wrapper.findAll('form')[0]!.trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith({ enabled: true, base_url: 'http://127.0.0.1:8319/v1', api_key: 'k'.repeat(32) })
    expect((wrapper.get('[data-testid="bridge-key"]').element as HTMLInputElement).value).toBe('')
    expect(api.test).not.toHaveBeenCalled()
  })
  it('submits exactly once and displays returned text without rendering HTML', async () => {
    api.test.mockResolvedValue({ success: true, model: 'gpt-6-luna', effort: 'high', text: '<img src=x onerror=alert(1)>', duration_ms: 1500, request_id: 'resp_test', usage_available: false })
    const wrapper = mount(PrismView)
    await flushPromises()
    await wrapper.get('[data-testid="model"]').setValue('gpt-6-luna')
    await wrapper.get('[data-testid="effort"]').setValue('high')
    await wrapper.findAll('form')[1]!.trigger('submit')
    await flushPromises()
    expect(api.test).toHaveBeenCalledTimes(1)
    expect(api.test).toHaveBeenCalledWith(7, { model: 'gpt-6-luna', effort: 'high', prompt: 'Reply with OK.' })
    expect(wrapper.get('pre').text()).toBe('<img src=x onerror=alert(1)>')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('本次不计费')
    expect(api.save).not.toHaveBeenCalled()
  })
  it('does not retry a model rejection or display success', async () => {
    api.test.mockRejectedValue(new Error('Prism: model_unavailable'))
    const wrapper = mount(PrismView)
    await flushPromises()
    await wrapper.findAll('form')[1]!.trigger('submit')
    await flushPromises()
    expect(api.test).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[role="alert"]').text()).toContain('model_unavailable')
    expect(wrapper.find('pre').exists()).toBe(false)
  })
})
