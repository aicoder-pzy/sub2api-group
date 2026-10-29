import { describe, expect, it } from 'vitest'
import * as XLSX from 'xlsx'
import { importBroadcastRecipients } from '../emailBroadcastImport'

const file = (name: string, bytes: ArrayBuffer) => ({ name, size: bytes.byteLength, arrayBuffer: async () => bytes } as File)

describe('Broadcast recipient imports', () => {
  it('keeps complete TXT entries and removes only the first known header', async () => {
    const bytes = new TextEncoder().encode('QQ号\r\n123456789，user@example.com;备注987654321').buffer
    expect(await importBroadcastRecipients(file('qq.txt', bytes))).toBe('123456789\nuser@example.com\n备注987654321')
  })
  it('reads only the first sheet and first column without turning other numbers into recipients', async () => {
    const book = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(book, XLSX.utils.aoa_to_sheet([['QQ号', 'amount'], [123456789, 888888], ['012345', 999999], ['user@example.com']]), 'Contacts')
    XLSX.utils.book_append_sheet(book, XLSX.utils.aoa_to_sheet([[987654321]]), 'Ignore')
    const bytes = XLSX.write(book, { type: 'array', bookType: 'xlsx' }) as ArrayBuffer
    expect(await importBroadcastRecipients(file('qq.xlsx', bytes))).toBe('123456789\n012345\nuser@example.com')
    expect(await importBroadcastRecipients(file('qq.csv', new TextEncoder().encode('email,other\nuser@example.com,123456789').buffer))).toBe('user@example.com')
  })
  it('rejects oversized files and too many rows before import', async () => {
    await expect(importBroadcastRecipients({ size: 2 * 1024 * 1024 + 1 } as File)).rejects.toThrow('fileTooLarge')
    await expect(importBroadcastRecipients(file('qq.txt', new TextEncoder().encode(Array(10001).fill('12345').join('\n')).buffer))).rejects.toThrow('tooManyRows')
    await expect(importBroadcastRecipients(file('qq.exe', new ArrayBuffer(0)))).rejects.toThrow('fileType')
  })
})
