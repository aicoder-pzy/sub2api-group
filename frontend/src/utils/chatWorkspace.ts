import DOMPurify from 'dompurify'
import { marked } from 'marked'
import type { ChatAttachment, ChatConversation } from '@/api/chat'

export const CHAT_MAX_FILES = 4
export const CHAT_MAX_INPUT = 120000

export async function readChatFile(file: File, signal: AbortSignal): Promise<ChatAttachment> {
  if (file.size > 5 * 1024 * 1024) throw new Error('size')
  const buffer = await file.arrayBuffer()
  if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
  return new Promise((resolve, reject) => {
    const worker = new Worker(new URL('./chatFiles.worker.ts', import.meta.url), { type: 'module' })
    const finish = (error?: Error, attachment?: ChatAttachment) => {
      clearTimeout(timer); signal.removeEventListener('abort', abort); worker.terminate()
      if (error) reject(error)
      else resolve(attachment!)
    }
    const abort = () => finish(new DOMException('Aborted', 'AbortError'))
    const timer = setTimeout(() => finish(new Error('timeout')), 10000)
    signal.addEventListener('abort', abort, { once: true })
    worker.onmessage = event => event.data.error ? finish(new Error(event.data.error)) : finish(undefined, event.data.attachment)
    worker.onerror = () => finish(new Error('parse'))
    worker.postMessage({ name: file.name, buffer }, [buffer])
  })
}

export function chatMarkdown(text: string): string {
  return DOMPurify.sanitize(marked.parse(text, { async: false, gfm: true, breaks: true }), {
    FORBID_TAGS: ['img', 'video', 'audio', 'iframe', 'style', 'form', 'input'], FORBID_ATTR: ['style']
  })
}

export function chatTables(text: string): string[][][] {
  const document = new DOMParser().parseFromString(chatMarkdown(text), 'text/html')
  return Array.from(document.querySelectorAll('table')).map(table =>
    Array.from(table.rows).map(row => Array.from(row.cells).map(cell => cell.textContent || '')))
}

/** Plain text cells: never turn model output into executable spreadsheet formulae. */
export function tableTSV(table: string[][]): string {
  return table.map(row => row.map(value => {
    const text = value.replace(/[\t\r\n]/g, ' ')
    return /^[\s]*[=+\-@]/.test(text) || /^0\d/.test(text) || /^\d{15,}$/.test(text) ? "'" + text : text
  }).join('\t')).join('\n')
}

export function chatTranscript(chat: ChatConversation): string {
  return '# ' + (chat.title || 'Chat') + '\n\n' + chat.messages.map(message =>
    `## ${message.role}\n\n${message.content}` + (message.attachments || []).map(file =>
      `\n\n### ${file.name}\n\n${file.content}`).join('')).join('\n\n')
}

export function downloadChatFile(content: BlobPart, filename: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }))
  const link = document.createElement('a')
  link.href = url; link.download = Array.from(filename.replace(/[<>:"/\\|?*]/g, '_')).map(char => char.charCodeAt(0) < 32 ? '_' : char).join('').slice(0, 100)
  document.body.appendChild(link); link.click(); link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
