import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import GroupSchedulingAgent from '../GroupSchedulingAgent.vue'
import { schedulingAgentAPI, type SchedulingStatus } from '@/api/admin/schedulingAgent'

vi.mock('@/api/admin/schedulingAgent', () => ({ schedulingAgentAPI: { status: vi.fn(), save: vi.fn(), run: vi.fn(), revoke: vi.fn() } }))

function fixture(): SchedulingStatus {
  return {
    policy: { pools: [
      { id: 'primary', name: '主力供应商', accounts: [1, 2], models: { astra: [2, 1] } },
      { id: 'backup', name: '备用供应商', accounts: [3], models: {} },
    ], accounts: [], models: {}, observe_models: ['astra'], agent_mode: 'auto', daily_calls: 12, min_wait_ms: 500 },
    run: { state: '', at: '', calls: 0, input_tokens: 0, output_tokens: 0, expires: '' },
    accounts: [1, 2, 3, 4].map(id => ({ id, name: `账号 ${id}`, site: 'relay.example', upstream_group: `组 ${id}`, available: true, models: ['astra'] })),
  }
}

async function openPanel() {
  const wrapper = mount(GroupSchedulingAgent, { props: { groupId: 24 } })
  wrapper.element.setAttribute('open', '')
  await wrapper.trigger('toggle')
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(schedulingAgentAPI.status).mockResolvedValue(fixture())
  vi.mocked(schedulingAgentAPI.save).mockImplementation(async (_group, policy) => ({ ...fixture(), policy }))
})

describe('upstream pool settings', () => {
  it('saves administrator pool and model member order without changing budgets', async () => {
    const wrapper = await openPanel()
    await wrapper.findAll('[aria-label="上移池"]')[1].trigger('click')
    await wrapper.find('[aria-label="上游池名称"]').setValue('新的主力池')
    await wrapper.findAll('[aria-label="上移模型成员"]')[1].trigger('click')
    await wrapper.findAll('button').find(button => button.text() === '保存调度策略')!.trigger('click')
    await flushPromises()
    expect(schedulingAgentAPI.save).toHaveBeenCalledWith(24, expect.objectContaining({
      pools: [
        expect.objectContaining({ id: 'backup', name: '新的主力池', accounts: [3] }),
        expect.objectContaining({ id: 'primary', accounts: [1, 2], models: { astra: [1, 2] } }),
      ], accounts: [], models: {}, observe_models: ['astra'], agent_mode: 'auto', daily_calls: 12, min_wait_ms: 500,
    }))
    wrapper.unmount()
  })

  it('does not allow assigning a member to two pools', async () => {
    const wrapper = await openPanel()
    const selects = wrapper.findAll('[aria-label="添加池成员"]')
    expect(selects[0].findAll('option').map(option => option.attributes('value'))).toEqual(['', '4'])
    await selects[0].setValue('4')
    expect(wrapper.findAll('[aria-label="添加池成员"]')[1].findAll('option').map(option => option.attributes('value'))).toEqual([''])
    wrapper.unmount()
  })

  it('preserves legacy routing when the administrator returns to account mode', async () => {
    const value = fixture()
    value.policy = { accounts: [1, 3], models: { astra: [2, 1] }, agent_mode: 'observe', daily_calls: 9, min_wait_ms: 500 }
    vi.mocked(schedulingAgentAPI.status).mockResolvedValue(value)
    const wrapper = await openPanel()
    await wrapper.find('[aria-label="调度层级"]').setValue('pools')
    await wrapper.find('[aria-label="调度层级"]').setValue('accounts')
    await wrapper.findAll('button').find(button => button.text() === '保存调度策略')!.trigger('click')
    await flushPromises()
    expect(schedulingAgentAPI.save).toHaveBeenCalledWith(24, expect.objectContaining({ accounts: [1, 3], models: { astra: [2, 1] }, pools: [], agent_mode: 'observe', daily_calls: 9 }))
    wrapper.unmount()
  })
})
