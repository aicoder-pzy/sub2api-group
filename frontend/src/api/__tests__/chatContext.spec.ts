import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { chatSchema, loadChats, saveChats, streamChat, type ChatConversation } from '../chat'

const image = { name: 'ledger.png', dataUrl: 'data:image/png;base64,AA==', width: 200, height: 300 }
const thread = (count = 14): ChatConversation => ({ id: 'financial-work', title: 'Ledger', keyId: 4,
  model: 'gpt-6.1-sol', effort: '', updatedAt: '', financeMode: true,
  messages: Array.from({ length: count }, (_, i) => ({ id: String(i), role: 'user', content: 'Source ' + i,
    reasoning: '', status: 'complete', images: [{ ...image, name: 'source-' + i + '.png' }] })) })
function response(text: string, reason = 'stop') {
  return new Response('data: ' + JSON.stringify({ choices: [{ index: 0, delta: { content: text }, finish_reason: null }] }) +
    '\n\ndata: ' + JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] }) + '\n\ndata: [DONE]\n\n',
  { headers: { 'Content-Type': 'text/event-stream' } })
}
const imageCount = (messages: { content: string | { type: string }[] }[]) =>
  messages.reduce((sum, m) => sum + (Array.isArray(m.content) ? m.content.filter(p => p.type === 'image_url').length : 0), 0)
let fetchMock: ReturnType<typeof vi.fn>
beforeEach(() => {
  localStorage.clear()
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  vi.stubGlobal('crypto', webcrypto)
})
afterEach(() => vi.unstubAllGlobals())

describe('automatic chat context management', () => {
  it('continues a 14-image conversation using a vision summary and recent originals without changing history', async () => {
    fetchMock.mockResolvedValueOnce(response('Confirmed: GBP, 49.99%, -270,937.50. Cause remains unverified.'))
      .mockResolvedValueOnce(response('Continue the reconciliation'))
    const chat = thread(), original = JSON.stringify(chat.messages), delta = vi.fn(), checkpoint = vi.fn(), compacting = vi.fn()
    expect(await streamChat(chat, 'SELECTED_KEY', new AbortController().signal, delta, { checkpoint, compacting })).toBe('complete')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const [summary, answer] = fetchMock.mock.calls.map(call => JSON.parse(call[1].body))
    expect(imageCount(summary.messages)).toBe(8)
    expect(imageCount(answer.messages)).toBe(6)
    expect(JSON.stringify(summary.messages)).toContain('source-0.png')
    expect(JSON.stringify(answer.messages)).toContain('source-13.png')
    expect(JSON.stringify(answer.messages)).toContain('-270,937.50')
    expect(JSON.stringify(summary.messages)).toContain('撤回')
    expect(summary.messages[0].content).not.toContain('禁止按差额倒挤分录')
    expect(answer.messages[0].role).toBe('system')
    expect(answer.messages[0].content).toContain('禁止按差额倒挤分录')
    expect(JSON.stringify(chat.messages)).toBe(original)
    expect(delta).toHaveBeenCalledWith('Continue the reconciliation', '')
    expect(delta).not.toHaveBeenCalledWith(expect.stringContaining('Confirmed:'), expect.anything())
    expect(compacting).toHaveBeenCalledWith(true)
    expect(compacting.mock.lastCall).toEqual([false])
    expect(checkpoint).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls.every(call => call[1].headers.Authorization === 'Bearer SELECTED_KEY')).toBe(true)
    expect(fetchMock.mock.calls[0][1].headers.session_id).toBe('web-chat-financial-work-context')
    expect(fetchMock.mock.calls[1][1].headers.session_id).toBe('web-chat-financial-work')
  })

  it('checkpoints each batch and reuses completed work after an answer failure and a browser reload', async () => {
    const chat = thread(24)
    fetchMock.mockResolvedValueOnce(response('First evidence summary')).mockResolvedValueOnce(response('Merged evidence summary'))
      .mockResolvedValueOnce(new Response('', { status: 503 }))
    const checkpoint = vi.fn(async () => { await saveChats(1, [chat]) })
    await expect(streamChat(chat, 'key', new AbortController().signal, () => {}, { checkpoint })).rejects.toMatchObject({ code: 'http' })
    expect(checkpoint).toHaveBeenCalledTimes(2)
    const restored = (await loadChats(1))[0]!
    expect(restored.context?.summary).toBe('Merged evidence summary')
    expect(restored.messages).toHaveLength(24)
    fetchMock.mockClear().mockResolvedValueOnce(response('Answer after retry'))
    await streamChat(restored, 'key', new AbortController().signal, () => {})
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(JSON.stringify(JSON.parse(fetchMock.mock.calls[0][1].body).messages)).toContain('Merged evidence summary')
    expect((await loadChats(2))).toEqual([])
    expect(JSON.stringify(chatSchema(restored))).not.toContain('Bearer')
  })

  it('invalidates a summary when an older source is edited rather than reusing stale facts', async () => {
    const chat = thread()
    fetchMock.mockResolvedValueOnce(response('Old balance')).mockResolvedValueOnce(response('Old answer'))
    await streamChat(chat, 'key', new AbortController().signal, () => {})
    chat.messages[0]!.content = 'CORRECTED balance: -00120.50 GBP'
    fetchMock.mockClear().mockResolvedValueOnce(response('Corrected balance summary')).mockResolvedValueOnce(response('New answer'))
    await streamChat(chat, 'key', new AbortController().signal, () => {})
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const summary = JSON.parse(fetchMock.mock.calls[0][1].body)
    expect(JSON.stringify(summary.messages)).toContain('CORRECTED balance: -00120.50 GBP')
    expect(JSON.stringify(summary.messages)).not.toContain('Old balance')
    expect(chat.context?.summary).toBe('Corrected balance summary')
  })

  it.each(['length', 'content_filter'])('does not replace memory or send an answer using a truncated summary (%s)', async reason => {
    const chat = thread(), original = JSON.stringify(chat.messages), checkpoint = vi.fn()
    fetchMock.mockResolvedValueOnce(response('Incomplete evidence', reason))
    await expect(streamChat(chat, 'key', new AbortController().signal, () => {}, { checkpoint })).rejects.toMatchObject({ code: 'compaction' })
    expect(chat.context).toBeUndefined()
    expect(checkpoint).not.toHaveBeenCalled()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(JSON.stringify(chat.messages)).toBe(original)
  })

  it('stops during summarization while retaining a completed batch for the next attempt', async () => {
    const chat = thread(24), controller = new AbortController()
    fetchMock.mockResolvedValueOnce(response('Saved first batch'))
    await expect(streamChat(chat, 'key', controller.signal, () => {}, { checkpoint: () => controller.abort() }))
      .rejects.toMatchObject({ name: 'AbortError' })
    expect(chat.context?.summary).toBe('Saved first batch')
    expect(chat.context?.through).toBe(8)
    expect(chat.messages).toHaveLength(24)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('compacts accumulated text and retains the latest question and attachment verbatim', async () => {
    const chat = thread(4)
    chat.messages.forEach(m => { m.images = []; m.content = 'x'.repeat(60000) })
    chat.messages[3]!.content = 'Verify this exact amount'
    chat.messages[3]!.attachments = [{ name: 'accounts.csv', content: '0012,-120.50,GBP', sheets: [] }]
    fetchMock.mockResolvedValueOnce(response('Earlier text evidence')).mockResolvedValueOnce(response('Verified'))
    await streamChat(chat, 'key', new AbortController().signal, () => {})
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const payload = JSON.parse(fetchMock.mock.calls[1][1].body)
    expect(payload.messages.at(-1).content).toContain('0012,-120.50,GBP')
    expect(payload.messages.at(-1).content).toContain('Verify this exact amount')
    expect(chat.messages[0]!.content).toHaveLength(60000)
  })

  it('recovers from an explicit model context rejection before any answer, without replaying other errors', async () => {
    const chat = thread(3)
    fetchMock.mockResolvedValueOnce(new Response('{"error":{"code":"context_length_exceeded"}}', { status: 400 }))
      .mockResolvedValueOnce(response('Earlier sources summarized')).mockResolvedValueOnce(response('Continued answer'))
    const delta = vi.fn()
    await streamChat(chat, 'key', new AbortController().signal, delta)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(chat.context?.summary).toBe('Earlier sources summarized')
    expect(delta).toHaveBeenCalledWith('Continued answer', '')
    fetchMock.mockClear().mockResolvedValueOnce(new Response('Insufficient balance', { status: 400 }))
    await expect(streamChat(chat, 'key', new AbortController().signal, () => {})).rejects.toMatchObject({ code: 'http', status: 400 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
