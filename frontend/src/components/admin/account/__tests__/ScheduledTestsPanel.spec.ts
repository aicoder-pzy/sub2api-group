import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ScheduledTestsPanel from '../ScheduledTestsPanel.vue'

const mocks = vi.hoisted(() => ({
  listByAccount: vi.fn(), create: vi.fn(), update: vi.fn(), listResults: vi.fn(), getAll: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: {
  scheduledTests: mocks, groups: { getAll: mocks.getAll }
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('@/utils/format', () => ({ formatDateTime: (value: string) => value }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({
  t: (key: string) => key, te: (key: string) => key.endsWith('test_request_failed')
}) }))

const plan = {
  id: 1, account_id: 5, model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *',
  enabled: true, max_results: 100, auto_recover: false, test_prompt: 'Question',
  expected_answer: '21', reasoning_effort: 'xhigh', judge_group_id: 2,
  judge_model_id: 'gpt-4.1-mini', judge_prompt: 'Compare answers'
}

async function openPanel() {
  const wrapper = mount(ScheduledTestsPanel, {
    props: { show: false, accountId: 5, accountPlatform: 'openai', modelOptions: [] },
    global: { stubs: {
      BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true,
      HelpTooltip: true, Icon: true, Toggle: true,
      Select: { props: ['modelValue'], template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />' },
      Input: { props: ['modelValue', 'placeholder'], template: '<input :placeholder="placeholder" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />' }
    } }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

describe('scheduled answer quality checks', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.listByAccount.mockResolvedValue([plan])
    mocks.getAll.mockResolvedValue([{ id: 2, name: 'Independent judge' }])
    mocks.update.mockImplementation(async (_id, fields) => ({ ...plan, ...fields }))
    mocks.listResults.mockResolvedValue([
      { id: 1, status: 'failed', quality_verdict: 'incorrect', quality_reason: 'Different answer', judge_account_id: 9,
        error_message: 'answer_mismatch', response_text: 'Wrong original answer', started_at: '2026-10-08T00:00:00Z' },
      { id: 2, status: 'unknown', quality_verdict: 'unknown', quality_reason: 'test_request_failed',
        error_message: 'Timeout', response_text: '', started_at: '2026-10-08T00:00:00Z' }
    ])
  })

  it('preserves configured fields on edit and clears judging when quality checks are disabled', async () => {
    const wrapper = await openPanel()
    expect(mocks.getAll).toHaveBeenCalledWith('openai')
    await wrapper.get('button[title="admin.scheduledTests.editPlan"]').trigger('click')
    const expected = wrapper.findAll('label').find(label => label.text().includes('admin.scheduledTests.expectedAnswer'))!
    expect((expected.get('textarea').element as HTMLTextAreaElement).value).toBe('21')
    const save = () => wrapper.findAll('button').find(button => button.text() === 'common.save')!
    await save().trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenLastCalledWith(1, expect.objectContaining({
      test_prompt: 'Question', expected_answer: '21', reasoning_effort: 'xhigh',
      judge_group_id: 2, judge_model_id: 'gpt-4.1-mini', judge_prompt: 'Compare answers'
    }))
    await wrapper.get('button[title="admin.scheduledTests.editPlan"]').trigger('click')
    await wrapper.findAll('label').find(label => label.text().includes('admin.scheduledTests.expectedAnswer'))!.get('textarea').setValue('')
    await save().trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenLastCalledWith(1, expect.objectContaining({
      expected_answer: '', judge_group_id: 0, judge_model_id: '', judge_prompt: ''
    }))
  })

  it('shows an inconclusive verdict separately and retains the raw wrong answer', async () => {
    const wrapper = await openPanel()
    await wrapper.get('.cursor-pointer').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.scheduledTests.qualityIncorrect')
    expect(wrapper.text()).toContain('admin.scheduledTests.qualityUnknown')
    expect(wrapper.text()).toContain('admin.scheduledTests.qualityReasons.test_request_failed')
    expect(wrapper.text()).toContain('Different answer')
    const response = wrapper.findAll('.cursor-pointer').find(item => item.text().includes('admin.scheduledTests.responseText'))!
    await response.trigger('click')
    expect(wrapper.text()).toContain('Wrong original answer')
    expect(wrapper.text()).not.toContain('answer_mismatch')
  })
})
