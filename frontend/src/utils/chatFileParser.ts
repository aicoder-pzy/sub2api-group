import * as XLSX from 'xlsx'
import type { ChatAttachment } from '@/api/chat'

export const CHAT_FILE_BYTES = 5 * 1024 * 1024
export const CHAT_SOURCE_CHARS = 100000
const MAX_ROWS = 2000, MAX_COLUMNS = 60, MAX_CELLS = 30000

export function parseChatFile(name: string, buffer: ArrayBuffer): ChatAttachment {
  if (buffer.byteLength > CHAT_FILE_BYTES) throw new Error('size')
  const extension = name.split('.').at(-1)?.toLowerCase()
  if (!['xlsx', 'xls', 'csv', 'tsv', 'txt', 'md'].includes(extension || '')) throw new Error('unsupported')
  let text = ''
  if (!['xlsx', 'xls'].includes(extension!)) {
    try { text = new TextDecoder('utf-8', { fatal: true }).decode(buffer) }
    catch { text = new TextDecoder('gb18030', { fatal: true }).decode(buffer) }
    text = text.replace(/^\uFEFF/, '')
    if (text.length > CHAT_SOURCE_CHARS) throw new Error('content')
    if (!text.trim()) throw new Error('empty')
    if (extension === 'txt' || extension === 'md') return { name, content: text, sheets: [] }
  }
  // Read only a bounded number of rows; !fullref is checked before accepting the file.
  // Formulae/macros are never executed. Keep formatted text (dates, account IDs, leading zeroes).
  const book = XLSX.read(text || buffer, { type: text ? 'string' : 'array', raw: true,
    sheetRows: MAX_ROWS + 1, sheetStubs: true, cellText: true, cellFormula: true, bookVBA: false, bookFiles: extension === 'xlsx' })
  // SheetJS can synthesize zero for an OOXML formula without a cached <v>.
  // Inspect its existing ZIP entries so an absent result is never reported as a real zero.
  const files = (book as XLSX.WorkBook & { files?: Record<string, { content?: Uint8Array }> }).files
  for (const [path, file] of Object.entries(files || {})) {
    if (!/(?:^|\/)worksheets\/[^/]+\.xml$/.test(path) || !file.content) continue
    const xml = new TextDecoder().decode(file.content).replace(/<!--[\s\S]*?-->/g, '')
    for (const [cell] of xml.matchAll(/<(?:\w+:)?c(?=[\s>])[^>]*>[\s\S]*?<\/(?:\w+:)?c\s*>/g)) {
      if (/<(?:\w+:)?f[\s/>]/.test(cell) && !/<(?:\w+:)?v(?:\s[^>]*)?>\s*[^<\s][^<]*<\/(?:\w+:)?v\s*>/.test(cell)) throw new Error('formula')
    }
  }
  const sheets: ChatAttachment['sheets'] = []
  const sections: string[] = []
  let cells = 0, characters = 0
  if (book.SheetNames.length > 12) throw new Error('sheets')
  for (const [index, sheetName] of book.SheetNames.entries()) {
    if (book.Workbook?.Sheets?.[index]?.Hidden) continue
    const sheet = book.Sheets[sheetName]
    const ref = sheet?.['!fullref'] || sheet?.['!ref']
    if (!sheet || !ref) continue
    const range = XLSX.utils.decode_range(ref)
    // Limits include the actual row/column positions so sparse malicious ranges are bounded too.
    if (range.e.r >= MAX_ROWS) throw new Error('rows')
    if (range.e.c >= MAX_COLUMNS) throw new Error('columns')
    cells += (range.e.r + 1) * (range.e.c + 1)
    if (cells > MAX_CELLS) throw new Error('cells')
    const lines = [`Sheet ${JSON.stringify(sheetName)} (${range.e.r + 1} rows, ${range.e.c + 1} columns)`]
    for (let row = 0; row <= range.e.r; row++) {
      const values: string[] = []
      for (let column = 0; column <= range.e.c; column++) {
        const cell = sheet[XLSX.utils.encode_cell({ r: row, c: column })] as XLSX.CellObject | undefined
        if (cell?.t === 'e' || (cell?.f && cell.v == null)) throw new Error('formula')
        const value = cell ? String(cell.w ?? cell.v ?? '') : ''
        values.push(cell?.f ? value + ' [formula: =' + cell.f + ']' : value)
      }
      if (!values.some(Boolean)) continue
      const line = `Row ${row + 1}: ${JSON.stringify(values)}`
      characters += line.length + 1
      if (characters > CHAT_SOURCE_CHARS) throw new Error('content')
      lines.push(line)
    }
    if (lines.length === 1) continue
    sheets.push({ name: sheetName, rows: range.e.r + 1, columns: range.e.c + 1 })
    sections.push(lines.join('\n'))
  }
  const content = sections.join('\n\n')
  if (!content || !sheets.length) throw new Error('empty')
  if (content.length > CHAT_SOURCE_CHARS) throw new Error('content')
  return { name, content, sheets }
}
