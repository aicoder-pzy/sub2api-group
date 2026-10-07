import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ModelAccountRoutingDialog from '../ModelAccountRoutingDialog.vue'
import { getModelAccountRouting, updateModelAccountRouting } from '@/api/admin/settings'
import { list } from '@/api/admin/accounts'

vi.mock('@/api/admin/settings', () => ({ getModelAccountRouting: vi.fn(), updateModelAccountRouting: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function mountDialog(selectedIds: number[] = []) {
  return mount(ModelAccountRoutingDialog, {
    props: { selectedIds },
    global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' }, Icon: true } }
  })
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getModelAccountRouting).mockResolvedValue({ rules: [{ model: 'gpt-6-astra', account_ids: [1] }] })
  vi.mocked(updateModelAccountRouting).mockImplementation(async settings => settings)
  vi.mocked(list).mockResolvedValue({ items: [
    { id: 1, name: 'Account A', platform: 'openai', status: 'active', schedulable: true },
    { id: 2, name: 'Account B', platform: 'openai', status: 'active', schedulable: true }
  ], total: 2 } as Awaited<ReturnType<typeof list>>)
})

describe('global model scheduling allowlists', () => {
  it('edits independent model allowlists and persists only after Save', async () => {
    const wrapper = mountDialog([2])
    await flushPromises()
    expect(wrapper.get('input[type="checkbox"]').element).toHaveProperty('checked', true)
    await wrapper.findAll('input[type="checkbox"]')[1]!.setValue(true)
    await wrapper.get('input[maxlength="200"]').setValue(' gpt-6-astra ')
    await wrapper.findAll('button').find(button => button.text().includes('modelRouting.add'))!.trigger('click')
    await wrapper.get('input[maxlength="200"]').setValue('gpt-6.1-sol')
    expect(updateModelAccountRouting).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text().includes('common.save'))!.trigger('click')
    await flushPromises()
    expect(updateModelAccountRouting).toHaveBeenCalledWith({ rules: [
      { model: 'gpt-6-astra', account_ids: [1, 2] }, { model: 'gpt-6.1-sol', account_ids: [2] }
    ] })
    expect(wrapper.emitted('updated')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('keeps selections across pages and allows an explicit empty denylist', async () => {
    vi.mocked(list).mockImplementation(async page => ({ items: [
      { id: page === 1 ? 1 : 21, name: `Account ${page}`, platform: 'openai', status: 'active', schedulable: true }
    ], total: 21 } as Awaited<ReturnType<typeof list>>))
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[aria-label="common.next"]').trigger('click')
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    expect(wrapper.text()).toContain('#21')
    await wrapper.get('[aria-label="common.back"]').trigger('click')
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await wrapper.get('[aria-label="admin.accounts.modelRouting.removeAccount #21"]').trigger('click')
    expect(wrapper.text()).toContain('modelRouting.blocked')
    await wrapper.findAll('button').find(button => button.text().includes('common.save'))!.trigger('click')
    await flushPromises()
    expect(updateModelAccountRouting).toHaveBeenCalledWith({ rules: [{ model: 'gpt-6-astra', account_ids: [] }] })
    wrapper.unmount()
  })

  it('does not overwrite settings on a load error or report a failed save as success', async () => {
    vi.mocked(getModelAccountRouting).mockRejectedValueOnce(new Error('offline'))
    let wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('offline')
    expect(wrapper.findAll('button').find(button => button.text().includes('common.save'))!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
    vi.mocked(updateModelAccountRouting).mockRejectedValueOnce(new Error('save failed'))
    wrapper = mountDialog()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('common.save'))!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('save failed')
    expect(wrapper.emitted('updated')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })

  it('rejects wildcard IDs and removes a restriction only when explicitly deleted', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('input[maxlength="200"]').setValue('gpt-*')
    await wrapper.findAll('button').find(button => button.text().includes('common.save'))!.trigger('click')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(updateModelAccountRouting).not.toHaveBeenCalled()
    await wrapper.get('[aria-label="admin.accounts.modelRouting.remove"]').trigger('click')
    await wrapper.findAll('button').find(button => button.text().includes('common.save'))!.trigger('click')
    await flushPromises()
    expect(updateModelAccountRouting).toHaveBeenCalledWith({ rules: [] })
    wrapper.unmount()
  })
})
