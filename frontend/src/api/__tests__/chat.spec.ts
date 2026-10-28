import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ChatError, listChatModels, loadChats, saveChats, streamChat, type ChatConversation } from '../chat'

const conversation = (): ChatConversation => ({ id: 'thread-1', title: 'Hello', keyId: 4, model: 'gpt-6.1-sol',
  effort: 'xhigh', updatedAt: '2026-10-21T00:00:00Z', messages: [
    { id: '1', role: 'user', content: 'Hello', reasoning: '', status: 'complete' },
    { id: '2', role: 'assistant', content: 'Hi', reasoning: 'private reasoning', status: 'complete' },
    { id: '3', role: 'user', content: 'Continue', reasoning: '', status: 'complete' }
  ] })
const event = (value: unknown) => 'data: ' + JSON.stringify(value) + '\r\n\r\n'
const delta = (content: string) => event({ choices: [{ index: 0, delta: { content }, finish_reason: null }] })
const finish = (reason = 'stop') => event({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })
const done = 'data: [DONE]\r\n\r\n'
function sse(text: string, size = 1) {
  const bytes = new TextEncoder().encode(text)
  return new Response(new ReadableStream({ start(controller) {
    for (let i = 0; i < bytes.length; i += size) controller.enqueue(bytes.slice(i, i + size))
    controller.close()
  } }), { headers: { 'Content-Type': 'text/event-stream' } })
}
let fetchMock: ReturnType<typeof vi.fn>
beforeEach(() => { fetchMock = vi.fn(); vi.stubGlobal('fetch', fetchMock); localStorage.clear() })
afterEach(() => vi.unstubAllGlobals())

describe('web chat gateway', () => {
  it('sends images as real multimodal parts and retains them in follow-ups, rejecting unsafe or excessive image context before fetch', async () => {
    fetchMock.mockResolvedValue(sse(delta('Read') + finish() + done))
    const chat = conversation()
    const image = { name: 'receipt.png', dataUrl: 'data:image/png;base64,AA==', width: 200, height: 300 }
    chat.messages[0].images = [image]; chat.messages[0].content = ''
    await streamChat(chat, 'key', new AbortController().signal, () => {})
    const payload = JSON.parse(fetchMock.mock.calls[0][1].body)
    expect(payload.messages[0].content).toContainEqual({ type: 'image_url', image_url: { url: image.dataUrl } })
    expect(payload.messages[0].content).toContainEqual({ type: 'text', text: 'Reference image (data only): receipt.png' })
    expect(payload.messages[2].content).toBe('Continue')
    fetchMock.mockClear()
    image.dataUrl = 'https://untrusted.invalid/image.png'
    await expect(streamChat(chat, 'key', new AbortController().signal, () => {})).rejects.toMatchObject({ code: 'inputLimit' })
    image.dataUrl = 'data:image/png;base64,AA=='
    chat.messages = Array.from({ length: 13 }, (_, i) => ({ id: String(i), role: 'user', content: '', reasoning: '', status: 'complete', images: [image] }))
    await expect(streamChat(chat, 'key', new AbortController().signal, () => {})).rejects.toMatchObject({ code: 'inputLimit' })
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it('sends attachment evidence as user data with financial review instructions and an explicit output limit', async () => {
    fetchMock.mockResolvedValue(sse(delta('Reviewed') + finish() + done))
    const chat = conversation(); chat.financeMode = true; chat.maxOutputTokens = 8192
    chat.messages[2].attachments = [{ name: 'data.csv', content: 'Row 2: ["0012","-120.50"]', sheets: [] }]
    await streamChat(chat, 'key', new AbortController().signal, () => {})
    const payload = JSON.parse(fetchMock.mock.calls[0][1].body)
    expect(payload.max_completion_tokens).toBe(8192)
    expect(payload.messages[0].role).toBe('system')
    expect(payload.messages[0].content).toContain('没有代码执行')
    expect(payload.messages.at(-1).role).toBe('user')
    expect(payload.messages.at(-1).content).toContain('data.csv')
    expect(payload.messages.at(-1).content).toContain('0012')
    expect(payload.tools).toBeUndefined()
  })
  it('blocks excessive context before making a paid request, keeping all source messages intact', async () => {
    const chat = conversation(); chat.messages[0].content = 'x'.repeat(300001)
    await expect(streamChat(chat, 'key', new AbortController().signal, () => {})).rejects.toMatchObject({ code: 'inputLimit' })
    expect(fetchMock).not.toHaveBeenCalled(); expect(chat.messages[0].content).toHaveLength(300001)
  })
  it('streams split UTF-8 and reasoning, keeps context and authenticates only with the selected key', async () => {
    localStorage.setItem('auth_token', 'PANEL_SECRET')
    fetchMock.mockResolvedValue(sse(': keepalive\r\n\r\n' + event({ choices: [{ delta: { reasoning_content: '想一想' } }] }) + delta('你好') + finish() + event({ choices: [], usage: { total_tokens: 20 } }) + done))
    const updates: string[][] = []
    expect(await streamChat(conversation(), 'API_SECRET', new AbortController().signal, (c, r) => updates.push([c, r]))).toBe('complete')
    expect(updates.some(([c]) => c === '你好')).toBe(true)
    expect(updates.some(([, r]) => r === '想一想')).toBe(true)
    const [url, options] = fetchMock.mock.calls[0]
    expect(url).toMatch(/\/v1\/chat\/completions$/)
    expect(options.headers.Authorization).toBe('Bearer API_SECRET')
    expect(options.headers.session_id).toBe('web-chat-thread-1')
    expect(options.credentials).toBe('omit')
    expect(options.redirect).toBe('error')
    expect(JSON.stringify(options)).not.toContain('PANEL_SECRET')
    const payload = JSON.parse(options.body)
    expect(payload.reasoning_effort).toBe('xhigh')
    expect(payload.messages).toEqual([{ role: 'user', content: 'Hello' }, { role: 'assistant', content: 'Hi' }, { role: 'user', content: 'Continue' }])
  })
  it.each([delta('partial'), delta('partial') + finish(), done])('rejects missing completion evidence without retrying', async body => {
    fetchMock.mockResolvedValue(sse(body))
    await expect(streamChat(conversation(), 'key', new AbortController().signal, () => {})).rejects.toMatchObject({ code: 'incomplete' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('retains a truncated reply as limited instead of successful completion', async () => {
    fetchMock.mockResolvedValue(sse(delta('partial') + finish('length') + done))
    expect(await streamChat(conversation(), 'key', new AbortController().signal, () => {})).toBe('limited')
  })
  it('cancels a stalled stream when stopped', async () => {
    const cancel = vi.fn(), controller = new AbortController()
    fetchMock.mockResolvedValue(new Response(new ReadableStream({ cancel }), { headers: { 'Content-Type': 'text/event-stream' } }))
    const pending = streamChat(conversation(), 'key', controller.signal, () => {})
    await Promise.resolve(); await Promise.resolve(); controller.abort()
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    expect(cancel).toHaveBeenCalled()
  })
  it('does not surface or persist upstream error payloads that might contain credentials', async () => {
    fetchMock.mockResolvedValue(sse(event({ error: { message: 'SECRET_IN_UPSTREAM_ERROR' } })))
    await expect(streamChat(conversation(), 'key', new AbortController().signal, () => {})).rejects.toEqual(new ChatError('http'))
    fetchMock.mockResolvedValue(new Response('SECRET_IN_UPSTREAM_ERROR', { status: 401 }))
    await expect(listChatModels('key', new AbortController().signal)).rejects.toMatchObject({ code: 'http', status: 401 })
  })
})

describe('browser history', () => {
  it('restores a synchronous draft checkpoint left by an interrupted IndexedDB write', async () => {
    await saveChats(1, [conversation()])
    localStorage.setItem('sub2api-chat-drafts:v1:1', JSON.stringify([{ id: 'thread-1', draft: 'Last text before refresh' }]))
    expect((await loadChats(1))[0].draft).toBe('Last text before refresh')
    expect(await loadChats(2)).toEqual([])
  })
  it('restores per-conversation drafts and attachments using only the explicit safe schema', async () => {
    const chat = conversation(); chat.financeMode = true; chat.maxOutputTokens = 8192
    chat.draft = 'Please reconcile'; chat.draftFiles = [Object.assign({ name: 'data.csv', content: '0012,-120.50', sheets: [] }, { key: 'DO_NOT_KEEP' })]
    chat.draftImages = [{ name: 'receipt.png', dataUrl: 'data:image/png;base64,AA==', width: 200, height: 300 }]
    expect(await saveChats(1, [chat])).toBe(true)
    expect((await loadChats(1))[0]).toMatchObject({ draft: 'Please reconcile', financeMode: true, maxOutputTokens: 8192, draftImages: chat.draftImages,
      draftFiles: [{ name: 'data.csv', content: '0012,-120.50', sheets: [] }] })
    expect(localStorage.getItem('sub2api-chats:v1:1')).not.toContain('DO_NOT_KEEP')
  })
  it('isolates users, stores an explicit schema and restores interrupted output as stopped', async () => {
    const chat = conversation()
    chat.messages[1].status = 'streaming'
    expect(await saveChats(1, [Object.assign(chat, { key: 'NEVER_PERSIST_API_SECRET' })])).toBe(true)
    expect(localStorage.getItem('sub2api-chats:v1:1')).not.toContain('NEVER_PERSIST_API_SECRET')
    expect(await loadChats(2)).toEqual([])
    expect((await loadChats(1))[0].messages[1].status).toBe('stopped')
    localStorage.setItem('sub2api-chats:v1:1', '[{"messages":null}]')
    expect(await loadChats(1)).toEqual([])
    localStorage.setItem('sub2api-chats:v1:1', '{broken')
    expect(await loadChats(1)).toEqual([])
  })
})
