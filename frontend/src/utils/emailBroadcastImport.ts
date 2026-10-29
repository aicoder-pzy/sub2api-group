const header = /^(qq号|qq号码|qq|邮箱|电子邮箱|email|email_address)$/i

export async function importBroadcastRecipients(file: File): Promise<string> {
  if (file.size > 2 * 1024 * 1024) throw new Error('fileTooLarge')
  const bytes = await file.arrayBuffer()
  const extension = file.name.split('.').pop()?.toLowerCase()
  let values: string[]
  if (extension === 'txt') {
    let text: string
    try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes) }
    catch { text = new TextDecoder('gb18030', { fatal: true }).decode(bytes) }
    values = text.trim().split(/[\s,;，；、]+/)
  } else if (['csv', 'xlsx', 'xls'].includes(extension || '')) {
    const XLSX = await import('xlsx')
    const workbook = XLSX.read(bytes, { type: 'array', sheetRows: 10002 })
    const sheet = workbook.Sheets[workbook.SheetNames[0]]
    if (!sheet) throw new Error('emptyFile')
    const range = XLSX.utils.decode_range(sheet['!fullref'] || sheet['!ref'] || 'A1')
    if (range.e.r > 10000) throw new Error('tooManyRows')
    values = XLSX.utils.sheet_to_json<unknown[]>(sheet, {
      header: 1, raw: false, blankrows: false, range: { s: { r: 0, c: 0 }, e: { r: range.e.r, c: 0 } }
    }).map(row => String(row[0] ?? '').trim()).filter(Boolean)
  } else throw new Error('fileType')
  if (header.test(values[0] || '')) values.shift()
  if (!values.length) throw new Error('emptyFile')
  if (values.length > 10000) throw new Error('tooManyRows')
  return values.join('\n')
}
