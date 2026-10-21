import { buildGatewayUrl } from '@/api/url'
import { consumeSSE, SSEError } from '@/utils/sse'

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  reasoning: string
  status: 'complete' | 'streaming' | 'stopped' | 'failed' | 'limited'
}

export interface ChatConversation {
  id: string
  title: string
  keyId: number | null
  model: string
  effort: string
  updatedAt: string
  messages: ChatMessage[]
}

export class ChatError extends Error {
  constructor(public code: 'network' | 'http' | 'invalidStream' | 'incomplete', public status?: number) {
    super(code)
  }
}

const object = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}

export async function listChatModels(key: string, signal: AbortSignal): Promise<string[]> {
  const response = await fetch(buildGatewayUrl('/v1/models'), {
    headers: { Authorization: 'Bearer ' + key }, signal, credentials: 'omit', cache: 'no-store', redirect: 'error'
  })
  if (!response.ok) { await response.body?.cancel(); throw new ChatError('http', response.status) }
  const value = object(await response.json())
  if (!Array.isArray(value.data)) throw new ChatError('invalidStream')
  return [...new Set(value.data.map(item => object(item).id).filter((id): id is string => typeof id === 'string'))].sort()
}

/** A single gateway request: retries remain the gateway's responsibility. */
export async function streamChat(
  conversation: ChatConversation, key: string, signal: AbortSignal,
  onDelta: (content: string, reasoning: string) => void
): Promise<'complete' | 'limited'> {
  const payload: Record<string, unknown> = {
    model: conversation.model, stream: true, stream_options: { include_usage: true }, max_completion_tokens: 4096,
    messages: conversation.messages.filter(m => m.content).map(({ role, content }) => ({ role, content }))
  }
  if (conversation.effort) payload.reasoning_effort = conversation.effort
  const response = await fetch(buildGatewayUrl('/v1/chat/completions'), {
    method: 'POST', headers: { Authorization: 'Bearer ' + key, 'Content-Type': 'application/json',
      Accept: 'text/event-stream', session_id: 'web-chat-' + conversation.id },
    body: JSON.stringify(payload), signal, credentials: 'omit', cache: 'no-store', redirect: 'error'
  })
  if (!response.ok) { await response.body?.cancel(); throw new ChatError('http', response.status) }
  let finished = false, limited = false
  try {
    await consumeSSE(response, value => {
      if (value === '[DONE]') {
        if (!finished) throw new ChatError('incomplete')
        return true
      }
      const event = object(value)
      if (event.error || event.type === 'error') throw new ChatError('http')
      if (Array.isArray(event.choices)) for (const choice of event.choices) {
        const item = object(choice)
        if (item.index !== undefined && item.index !== 0) continue
        const delta = object(item.delta), reasoning = delta.reasoning_content ?? delta.reasoning
        onDelta(typeof delta.content === 'string' ? delta.content : '', typeof reasoning === 'string' ? reasoning : '')
        if (typeof item.finish_reason === 'string' && item.finish_reason) {
          finished = ['stop', 'length', 'content_filter'].includes(item.finish_reason)
          limited = item.finish_reason !== 'stop'
        }
      }
      return false
    }, signal)
    return limited ? 'limited' : 'complete'
  } catch (error) {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    if (error instanceof SSEError) throw new ChatError(error.code === 'incomplete' ? 'incomplete' : 'invalidStream')
    throw error
  }
}

/** Persist message content and selection IDs only, never API credentials. */
export function saveChats(userId: number, conversations: ChatConversation[]): boolean {
  try {
    localStorage.setItem('sub2api-chats:v1:' + userId, JSON.stringify(conversations.map(c => ({
      id: c.id, title: c.title, keyId: c.keyId, model: c.model, effort: c.effort, updatedAt: c.updatedAt,
      messages: c.messages.map(m => ({ id: m.id, role: m.role, content: m.content, reasoning: m.reasoning,
        status: m.status === 'streaming' ? 'stopped' : m.status }))
    }))))
    return true
  } catch { return false }
}

export function loadChats(userId: number): ChatConversation[] {
  const validStatuses = ['complete', 'streaming', 'stopped', 'failed', 'limited']
  try {
    const value: unknown = JSON.parse(localStorage.getItem('sub2api-chats:v1:' + userId) || '[]')
    if (!Array.isArray(value)) return []
    return value.filter((c): c is ChatConversation => {
      const item = object(c)
      return typeof item.id === 'string' && typeof item.title === 'string' && typeof item.model === 'string' &&
        typeof item.effort === 'string' && typeof item.updatedAt === 'string' &&
        (item.keyId === null || (Number.isSafeInteger(item.keyId) && Number(item.keyId) > 0)) &&
        Array.isArray(item.messages) && item.messages.every(raw => {
          const m = object(raw)
          return typeof m.id === 'string' && ['user', 'assistant'].includes(String(m.role)) &&
            typeof m.content === 'string' && typeof m.reasoning === 'string' && validStatuses.includes(String(m.status))
        })
    }).map(c => ({ ...c, messages: c.messages.map(m => ({ ...m, status: m.status === 'streaming' ? 'stopped' : m.status })) }))
  } catch { return [] }
}
