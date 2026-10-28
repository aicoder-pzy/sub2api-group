import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import ChatView from '../ChatView.vue'
import { loadChats, saveChats, type ChatConversation } from '@/api/chat'

const mocks = vi.hoisted(() => ({ list: vi.fn(), key: vi.fn(), models: vi.fn(), stream: vi.fn(), readFile: vi.fn(), readImage: vi.fn(), auth: { user: { id: 1 } } }))
vi.mock('@/api/keys', () => ({ list: mocks.list, getById: mocks.key }))
vi.mock('@/api/chat', async () => ({ ...await vi.importActual<typeof import('@/api/chat')>('@/api/chat'), listChatModels: mocks.models, streamChat: mocks.stream }))
vi.mock('@/utils/chatWorkspace', async () => ({ ...await vi.importActual<typeof import('@/utils/chatWorkspace')>('@/utils/chatWorkspace'), readChatFile: mocks.readFile, readChatImage: mocks.readImage }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const key = { id: 4, user_id: 1, name: 'My key', key: 'NEVER_STORE_SECRET', group_id: 24, group: { name: 'My group' }, status: 'active', quota: 0, expires_at: null }
let wrapper: ReturnType<typeof mount>
const open = () => mount(ChatView, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
beforeEach(() => {
  vi.clearAllMocks(); localStorage.clear()
  mocks.auth = reactive({ user: { id: 1 } })
  mocks.list.mockResolvedValue({ items: [key, { ...key, id: 5, user_id: 2 }, { ...key, id: 6, status: 'inactive' }], pages: 1 })
  mocks.key.mockResolvedValue(key); mocks.models.mockResolvedValue(['gpt-6.1-sol'])
  mocks.stream.mockImplementation(async (_chat, _key, _signal, changed) => { changed('Hello **world**', ''); return 'complete' })
})
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals() })

describe('online chat', () => {
  it('keeps existing history untouched and blocks sending when browser storage cannot be read', async () => {
    localStorage.setItem('sub2api-chats:v1:1', 'EXISTING_HISTORY')
    vi.stubGlobal('indexedDB', { open: () => { throw new Error('Storage denied') } })
    wrapper = open(); await flushPromises()
    expect(wrapper.text()).toContain('chat.historyError')
    expect(wrapper.get('[data-testid="chat-send"]').attributes('disabled')).toBeDefined()
    expect(localStorage.getItem('sub2api-chats:v1:1')).toBe('EXISTING_HISTORY')
    expect(mocks.stream).not.toHaveBeenCalled()
  })
  it('pastes screenshots without sending, preserves text and submits an image-only message once', async () => {
    wrapper = open(); await flushPromises()
    const image = { name: 'screenshot.png', dataUrl: 'data:image/png;base64,AA==', width: 100, height: 200 }
    mocks.readImage.mockResolvedValue(image)
    await wrapper.get('[data-testid="chat-input"]').setValue('Keep this text')
    await wrapper.get('[data-testid="chat-input"]').trigger('paste', { clipboardData: { items: [
      { kind: 'file', getAsFile: () => new File(['image'], 'screenshot.png', { type: 'image/png' }) }
    ] } }); await flushPromises()
    expect(wrapper.get('[data-testid="chat-image-preview"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="chat-input"]').element).toHaveProperty('value', 'Keep this text')
    expect(mocks.stream).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="chat-input"]').setValue('')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.stream).toHaveBeenCalledTimes(1)
    expect(mocks.stream.mock.calls[0][0].messages[0]).toMatchObject({ content: '', images: [image] })
    expect((await loadChats(1))[0].messages[0].images).toEqual([image])
  })
  it('previews a source without invoking a model, submits it once, and restores unsent drafts', async () => {
    wrapper = open(); await flushPromises()
    mocks.readFile.mockResolvedValue({ name: 'ledger.csv', content: 'Row 2: ["0012","-120.50"]', sheets: [{ name: 'Ledger', rows: 2, columns: 2 }] })
    const file = wrapper.get('[data-testid="chat-file-input"]')
    Object.defineProperty(file.element, 'files', { configurable: true, value: [new File(['Account,Amount'], 'ledger.csv')] })
    await file.trigger('change'); await flushPromises()
    expect(wrapper.get('[data-testid="chat-file-preview"]').text()).toContain('0012')
    expect(mocks.stream).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="chat-task-reconcile"]').trigger('click')
    expect(wrapper.get('[data-testid="chat-input"]').element).toHaveProperty('value', 'chat.tasks.reconcile.prompt')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.stream).toHaveBeenCalledTimes(1)
    expect(mocks.stream.mock.calls[0][0].messages[0].attachments[0].content).toContain('0012')
    expect((await loadChats(1))[0].messages[0].attachments?.[0].name).toBe('ledger.csv')
    await wrapper.get('[data-testid="chat-input"]').setValue('Unsent follow-up')
    wrapper.unmount(); wrapper = open(); await flushPromises()
    expect(wrapper.get('[data-testid="chat-input"]').element).toHaveProperty('value', 'Unsent follow-up')
  })
  it('does not accept a partial batch when one source cannot be read', async () => {
    wrapper = open(); await flushPromises()
    mocks.readFile.mockResolvedValueOnce({ name: 'one.csv', content: 'Source', sheets: [] }).mockRejectedValueOnce(new Error('formula'))
    const input = wrapper.get('[data-testid="chat-file-input"]')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [new File(['x'], 'one.csv'), new File(['y'], 'two.xlsx')] })
    await input.trigger('change'); await flushPromises()
    expect(wrapper.text()).toContain('chat.files.formula')
    expect(wrapper.find('[data-testid="chat-file-preview"]').exists()).toBe(false)
    expect(mocks.stream).not.toHaveBeenCalled()
  })
  it('uses owned keys, sends context, renders safe Markdown and saves history without credentials', async () => {
    wrapper = open(); await flushPromises()
    expect(wrapper.get('[data-testid="chat-key"]').findAll('option')).toHaveLength(2)
    await wrapper.get('[data-testid="chat-input"]').setValue('Hello')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.stream).toHaveBeenCalledTimes(1)
    expect(wrapper.find('.chat-markdown strong').text()).toBe('world')
    expect(JSON.stringify(await loadChats(1))).not.toContain('NEVER_STORE_SECRET')
    await wrapper.get('[data-testid="chat-input"]').setValue('Continue')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.stream.mock.calls[1][0].messages.map((m: { role: string }) => m.role)).toEqual(['user', 'assistant', 'user'])
    expect(wrapper.text()).not.toContain('NEVER_STORE_SECRET')
  })
  it('rechecks ownership and group before a paid request', async () => {
    wrapper = open(); await flushPromises()
    mocks.key.mockResolvedValue({ ...key, group_id: 25 })
    await wrapper.get('[data-testid="chat-input"]').setValue('Hi')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.stream).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('chat.keyChanged')
    expect(wrapper.get('[data-testid="chat-input"]').element).toHaveProperty('value', 'Hi')
  })
  it('aborts generating on stop and does not send while IME composition is active', async () => {
    wrapper = open(); await flushPromises()
    let activeSignal: AbortSignal | undefined
    mocks.stream.mockImplementation((_chat, _key, signal) => new Promise((_resolve, reject) => {
      activeSignal = signal
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
    }))
    await wrapper.get('[data-testid="chat-input"]').setValue('你好')
    await wrapper.get('[data-testid="chat-input"]').trigger('keydown', { key: 'Enter', isComposing: true })
    expect(mocks.stream).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    await wrapper.get('[data-testid="chat-stop"]').trigger('click'); await flushPromises()
    expect(activeSignal?.aborted).toBe(true)
    expect((await loadChats(1))[0].messages.at(-1)?.status).toBe('stopped')
  })
  it('removes unsafe HTML and clears visible history after changing users', async () => {
    const history: ChatConversation = { id: 'old', title: 'User one secret', keyId: 4, model: 'gpt-6.1-sol', effort: '', updatedAt: '', messages: [
      { id: '1', role: 'assistant', status: 'complete', reasoning: '', content: '<img src="https://evil.invalid" onerror="alert(1)"><script>alert(1)</script>[bad](javascript:alert(1))' }
    ] }
    await saveChats(1, [history])
    wrapper = open(); await flushPromises()
    expect(wrapper.find('.chat-markdown img').exists()).toBe(false)
    expect(wrapper.find('.chat-markdown script').exists()).toBe(false)
    expect(wrapper.find('.chat-markdown a').attributes('href')).toBeUndefined()
    mocks.auth.user = { id: 2 }; await flushPromises()
    expect(wrapper.text()).not.toContain('User one secret')
    expect(mocks.stream).not.toHaveBeenCalled()
  })
  it('requires a new selection when a historical key is unavailable instead of silently changing billing groups', async () => {
    await saveChats(1, [{ id: 'old', title: 'Old group', keyId: 999, model: 'old-model', effort: '', updatedAt: '', messages: [
      { id: '1', role: 'user', status: 'complete', reasoning: '', content: 'Old question' }
    ] }])
    wrapper = open(); await flushPromises()
    expect(wrapper.get('[data-testid="chat-key"]').element).toHaveProperty('selectedIndex', 0)
    expect(wrapper.get('[data-testid="chat-send"]').attributes('disabled')).toBeDefined()
    expect(mocks.stream).not.toHaveBeenCalled()
  })
})
