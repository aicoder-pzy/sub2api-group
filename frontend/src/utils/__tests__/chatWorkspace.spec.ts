import { describe, expect, it } from 'vitest'
import * as XLSX from 'xlsx'
import { parseChatFile } from '../chatFileParser'
import { chatTables, chatTranscript, readChatImage, tableTSV } from '../chatWorkspace'

const bytes = (text: string) => new TextEncoder().encode(text).buffer
function workbook(sheet: XLSX.WorkSheet): ArrayBuffer {
  const book = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(book, sheet, 'Ledger')
  return XLSX.write(book, { type: 'array', bookType: 'xlsx' })
}

describe('finance source files and exports', () => {
  it('rejects oversized or unsupported photos before decoding and exports attached images with the transcript', async () => {
    await expect(readChatImage(new File(['x'], 'camera.heic', { type: 'image/heic' }), new AbortController().signal)).rejects.toThrow('imageFormat')
    const large = new File(['x'], 'photo.jpg', { type: 'image/jpeg' })
    Object.defineProperty(large, 'size', { value: 20 * 1024 * 1024 + 1 })
    await expect(readChatImage(large, new AbortController().signal)).rejects.toThrow('imageSize')
    expect(chatTranscript({ id: '1', title: '', keyId: 1, model: 'm', effort: '', updatedAt: '', messages: [
      { id: '1', role: 'user', content: '', reasoning: '', status: 'complete', images: [{ name: 'receipt.png', dataUrl: 'data:image/png;base64,AA==', width: 1, height: 1 }] }
    ] })).toContain('![Reference image](data:image/png;base64,AA==)')
  })
  it('preserves CSV identifiers, quoted commas, signs and source rows without number coercion', () => {
    const file = parseChatFile('statement.csv', bytes('\uFEFFAccount,Amount,Note\r\n00123456789012345678,-120.50,"Fee, refund"\r\n'))
    expect(file.sheets).toEqual([{ name: 'Sheet1', rows: 2, columns: 3 }])
    expect(file.content).toContain('Row 2: ["00123456789012345678","-120.50","Fee, refund"]')
  })
  it('keeps Excel display formats and cached formula evidence, rejects uncached or errored formulae', () => {
    const sheet = XLSX.utils.aoa_to_sheet([['Account', 'Amount', 'Total'], [12, -120.5]])
    sheet.A2.z = '000000'; sheet.B2.z = '0.00'; sheet.C2 = { t: 'n', v: -120.5, f: 'SUM(B2:B2)' }
    const file = parseChatFile('ledger.xlsx', workbook(sheet))
    expect(file.content).toContain('000012')
    expect(file.content).toContain('-120.50')
    expect(file.content).toContain('[formula: =SUM(B2:B2)]')
    sheet.C2 = { t: 'n', f: 'SUM(B2:B2)' }
    expect(() => parseChatFile('missing.xlsx', workbook(sheet))).toThrow('formula')
    const archive = XLSX.CFB.read(new Uint8Array(workbook(sheet)), { type: 'array' })
    const entry = archive.FileIndex.find(item => item.name === 'sheet1.xml')!
    entry.content = new TextEncoder().encode(new TextDecoder().decode(entry.content).replace(/t="n"/g, ''))
    expect(() => parseChatFile('implicit-zero.xlsx', XLSX.CFB.write(archive, { type: 'array', fileType: 'zip' }))).toThrow('formula')
    sheet.C2 = { t: 'e', v: 7 }
    expect(() => parseChatFile('error.xlsx', workbook(sheet))).toThrow('formula')
  })
  it('rejects whole files with excessive ranges rather than presenting a partial import', () => {
    const rows = XLSX.utils.aoa_to_sheet([['Header']]); rows['!ref'] = 'A1:A2001'; rows.A2001 = { t: 's', v: 'last row' }
    expect(() => parseChatFile('too-many-rows.xlsx', workbook(rows))).toThrow('rows')
    const columns = XLSX.utils.aoa_to_sheet([['Header']]); columns['!ref'] = 'A1:BI1'; columns.BI1 = { t: 's', v: 'last column' }
    expect(() => parseChatFile('too-wide.xlsx', workbook(columns))).toThrow('columns')
    expect(() => parseChatFile('invoice.pdf', bytes('not a supported file'))).toThrow('unsupported')
  })
  it('exports table text safely, preserving IDs and treating formula-like strings as data', () => {
    const tables = chatTables('| Account | Amount |\n| --- | --- |\n| **0012** | -100.50 |\n| =HYPERLINK("https://example.invalid") | 20 |\n\n| Second |\n| --- |\n| More |')
    expect(tables).toHaveLength(2)
    expect(tables[0][1]).toEqual(['0012', '-100.50'])
    expect(tableTSV(tables[0])).toContain("'0012\t'-100.50")
    const sheet = XLSX.utils.aoa_to_sheet(tables[0])
    expect(sheet.A3.t).toBe('s'); expect(sheet.A3.f).toBeUndefined()
    expect(chatTables('<img src="https://example.invalid"><script>evil()</script>')).toEqual([])
  })
  it('includes submitted sources in the exported conversation without credentials', () => {
    const transcript = chatTranscript({ id: 'one', title: 'Report', keyId: 4, model: 'model', effort: '', updatedAt: '', messages: [
      { id: 'm', role: 'user', status: 'complete', reasoning: '', content: 'Review', attachments: [{ name: 'data.csv', content: 'source rows', sheets: [] }] }
    ] })
    expect(transcript).toContain('source rows'); expect(transcript).toContain('data.csv')
    expect(transcript).not.toContain('keyId')
  })
})
