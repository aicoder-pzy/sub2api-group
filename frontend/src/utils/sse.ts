export class SSEError extends Error {
  constructor(public code: 'invalid_stream' | 'incomplete' | 'aborted') { super(code) }
}

/** Incremental SSE parsing shared by chat and gateway cache tests. */
export async function consumeSSE(response: Response, onData: (value: unknown) => boolean, signal: AbortSignal): Promise<void> {
  if (!response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw new SSEError('invalid_stream')
  const reader = response.body.getReader(), decoder = new TextDecoder()
  let buffer = '', data: string[] = [], done = false
  const dispatch = () => {
    if (!data.length) return
    const payload = data.join('\n')
    data = []
    if (payload === '[DONE]') { done = onData('[DONE]'); return }
    let parsed: unknown
    try { parsed = JSON.parse(payload) } catch { throw new SSEError('invalid_stream') }
    done = onData(parsed)
  }
  const line = (raw: string) => {
    const value = raw.endsWith('\r') ? raw.slice(0, -1) : raw
    if (!value) dispatch()
    else if (value.startsWith('data:')) data.push(value.slice(5).replace(/^ /, ''))
  }
  const cancel = () => { void reader.cancel().catch(() => {}) }
  signal.addEventListener('abort', cancel, { once: true })
  try {
    while (!done) {
      if (signal.aborted) throw new SSEError('aborted')
      const chunk = await reader.read()
      buffer += decoder.decode(chunk.value, { stream: !chunk.done })
      if (buffer.length + data.reduce((n, d) => n + d.length, 0) > 2_000_000) throw new SSEError('invalid_stream')
      let end: number
      while (!done && (end = buffer.indexOf('\n')) >= 0) {
        line(buffer.slice(0, end)); buffer = buffer.slice(end + 1)
      }
      if (chunk.done) {
        if (buffer && !done) line(buffer)
        if (!done) dispatch()
        break
      }
    }
    if (signal.aborted) throw new SSEError('aborted')
    if (!done) throw new SSEError('incomplete')
  } finally {
    signal.removeEventListener('abort', cancel)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
