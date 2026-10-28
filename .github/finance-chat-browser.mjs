// Browser regression with synthetic financial data and intercepted APIs: no paid model calls.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'

const require = createRequire(path.resolve(process.env.BROWSER_TEST_MODULES, 'probe.cjs'))
const { chromium } = require('playwright')
const XLSX = createRequire(path.resolve('frontend/package.json'))('xlsx')
const output = path.resolve('finance-browser-results')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
try {
  const context = await browser.newContext({ viewport: { width: 1366, height: 900 }, locale: 'zh-CN' })
  const user = { id: 1, role: 'user', email: 'finance@example.invalid', username: 'Finance', balance: 100, status: 'active', concurrency: 5 }
  const key = { id: 4, user_id: 1, name: 'Finance', key: 'TEST_KEY', group_id: 24, group: { id: 24, name: '财务办公' }, status: 'active', quota: 0, quota_used: 0 }
  const requests = [], errors = []
  await context.addInitScript(({ user }) => {
    localStorage.setItem('auth_token', 'TEST_PANEL_TOKEN')
    localStorage.setItem('auth_user', JSON.stringify(user))
    localStorage.setItem('sub2api_locale', 'zh')
    if (!localStorage.getItem('chat-legacy-seeded')) {
      localStorage.setItem('chat-legacy-seeded', '1')
      localStorage.setItem('sub2api-chats:v1:1', JSON.stringify([{ id: 'legacy-chat', title: '', keyId: 4,
        model: 'gpt-6.1-sol', effort: '', financeMode: true, updatedAt: '', messages: [] }]))
    }
  }, { user })
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== 'http://127.0.0.1:4173') return route.abort()
    const json = data => route.fulfill({ json: { code: 0, data } })
    if (url.pathname === '/v1/models') return route.fulfill({ json: { data: [{ id: 'gpt-6.1-sol' }] } })
    if (url.pathname === '/v1/chat/completions') {
      assert.equal(request.headers().authorization, 'Bearer TEST_KEY')
      const payload = request.postDataJSON(); requests.push(payload)
      assert.equal(payload.messages[0].role, 'system')
      const answer = '| 单号 | 差额 |\n| --- | --- |\n| 0012 | -120.50 |\n\n## 核对结论\n请按来源逐项复核。'
      const chunk = data => 'data: ' + JSON.stringify(data) + '\n\n'
      return route.fulfill({ contentType: 'text/event-stream', body:
        chunk({ choices: [{ index: 0, delta: { content: answer }, finish_reason: null }] }) +
        chunk({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] }) + 'data: [DONE]\n\n' })
    }
    if (url.pathname === '/api/v1/auth/me') return json(user)
    if (url.pathname === '/api/v1/keys') return json({ items: [key], total: 1, pages: 1, page: 1, page_size: 100 })
    if (url.pathname === '/api/v1/keys/4') return json(key)
    if (url.pathname === '/api/v1/settings/public') return json({ site_name: '财务办公', backend_mode_enabled: false, custom_menu_items: [] })
    if (url.pathname.includes('/setup/status')) return json({ needs_setup: false })
    if (url.pathname.startsWith('/api/')) return json([])
    return route.continue()
  })
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('http://127.0.0.1:4173/chat')
  await page.getByTestId('chat-task-reconcile').waitFor()
  await page.screenshot({ path: path.join(output, 'desktop-start.png') })
  await page.getByTestId('chat-task-reconcile').click()
  const sourceBook = XLSX.utils.book_new(), sourceSheet = XLSX.utils.aoa_to_sheet([['单号', '金额'], [12, -120.5]])
  sourceSheet.A2.z = '000000'; sourceSheet.B2.z = '0.00'
  XLSX.utils.book_append_sheet(sourceBook, sourceSheet, '总账')
  await page.getByTestId('chat-file-input').setInputFiles([
    { name: 'bank.csv', mimeType: 'text/csv', buffer: Buffer.from('单号,金额\n0012,-120.50\n') },
    { name: 'ledger.xlsx', mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', buffer: XLSX.write(sourceBook, { type: 'buffer', bookType: 'xlsx' }) }
  ])
  await page.getByTestId('chat-file-preview').nth(1).waitFor({ state: 'attached' })
  assert.match(await page.getByTestId('chat-file-preview').nth(0).textContent(), /0012/)
  assert.match(await page.getByTestId('chat-file-preview').nth(1).textContent(), /000012/)
  assert.equal(requests.length, 0, 'Reading files must not call a model')
  await page.getByTestId('chat-send').click()
  await page.getByTestId('chat-download-excel').waitFor()
  assert.equal(requests.length, 1)
  assert.match(requests[0].messages.at(-1).content, /bank\.csv/)
  assert.match(requests[0].messages.at(-1).content, /ledger\.xlsx/)
  await page.screenshot({ path: path.join(output, 'desktop-result.png') })
  const downloadEvent = page.waitForEvent('download')
  await page.getByTestId('chat-download-excel').click()
  const download = await downloadEvent
  assert.match(download.suggestedFilename(), /\.xlsx$/)
  const exported = XLSX.read(await readFile(await download.path()), { type: 'buffer' })
  assert.equal(exported.Sheets['Table 1'].A2.v, '0012')
  assert.equal(exported.Sheets['Table 1'].B2.v, '-120.50')
  assert.equal(exported.Sheets['Table 1'].A2.t, 's')
  const reportEvent = page.waitForEvent('download')
  await page.getByRole('button', { name: '下载报告（网页）', exact: true }).click()
  const report = await reportEvent
  assert.match(report.suggestedFilename(), /\.html$/)
  assert.match(await readFile(await report.path(), 'utf8'), /Content-Security-Policy/)
  await page.getByTestId('chat-input').fill('刷新后继续核对')
  await page.reload()
  await page.getByTestId('chat-input').waitFor()
  assert.equal(await page.getByTestId('chat-input').inputValue(), '刷新后继续核对')
  assert.match(await page.locator('.chat-markdown').textContent(), /0012/)
  assert.equal(requests.length, 1, 'Reload and exports must not call a model')
  // Generate an oversized-edge screenshot in Chromium; exercise the actual browser image decoder/canvas.
  const screenshotData = await page.evaluate(() => {
    const canvas = document.createElement('canvas'); canvas.width = 3200; canvas.height = 1200
    const ctx = canvas.getContext('2d'); ctx.fillStyle = 'white'; ctx.fillRect(0, 0, 3200, 1200)
    ctx.fillStyle = '#111'; ctx.font = '90px sans-serif'; ctx.fillText('Receipt 0012   CNY -120.50', 100, 300)
    return canvas.toDataURL('image/png')
  })
  await page.getByTestId('chat-input').fill('')
  await page.getByTestId('chat-input').evaluate((input, data) => {
    const bytes = Uint8Array.from(atob(data.split(',')[1]), char => char.charCodeAt(0))
    const transfer = new DataTransfer(); transfer.items.add(new File([bytes], 'screenshot.png', { type: 'image/png' }))
    input.dispatchEvent(new ClipboardEvent('paste', { clipboardData: transfer, bubbles: true, cancelable: true }))
  }, screenshotData)
  await page.getByTestId('chat-image-preview').waitFor()
  await page.getByTestId('chat-image-preview').click()
  await page.locator('dialog[open]').waitFor()
  assert.match(await page.locator('dialog img').getAttribute('src'), /^data:image\/png;base64,/)
  await page.getByRole('button', { name: '关闭预览' }).click()
  assert.equal(requests.length, 1, 'Pasting/previewing must not call a model')
  await page.reload()
  await page.getByTestId('chat-image-preview').waitFor()
  assert.equal(await page.getByTestId('chat-input').inputValue(), '')
  assert.match(await page.getByTestId('chat-image-preview').textContent(), /2560 × 960/)
  await page.screenshot({ path: path.join(output, 'desktop-image-draft.png') })
  await page.getByTestId('chat-send').click()
  await page.getByTestId('chat-download-excel').nth(1).waitFor()
  assert.equal(requests.length, 2)
  const parts = requests[1].messages.at(-1).content
  assert.ok(Array.isArray(parts)); assert.ok(parts.some(part => part.type === 'image_url' && part.image_url.url.startsWith('data:image/png;base64,')))
  await page.getByTestId('chat-input').fill('核对图片中的金额与单号')
  await page.getByTestId('chat-send').click()
  await page.getByTestId('chat-download-excel').nth(2).waitFor()
  assert.equal(requests.length, 3)
  assert.ok(requests[2].messages.some(message => Array.isArray(message.content) && message.content.some(part => part.type === 'image_url')))
  const stored = await page.evaluate(() => new Promise((resolve, reject) => {
    const open = indexedDB.open('sub2api-chat-history', 1)
    open.onerror = () => reject(open.error)
    open.onsuccess = () => {
      const db = open.result, request = db.transaction('users').objectStore('users').get(1)
      request.onsuccess = () => { db.close(); resolve({ value: request.result, legacy: localStorage.getItem('sub2api-chats:v1:1') }) }
    }
  }))
  assert.equal(stored.legacy, null); assert.match(stored.value, /legacy-chat/); assert.match(stored.value, /data:image\/png;base64,/)
  assert.ok(!stored.value.includes('TEST_KEY') && !stored.value.includes('TEST_PANEL_TOKEN'))
  // Failed mixed batches never partially attach the readable source.
  await page.getByTestId('chat-file-input').setInputFiles([
    { name: 'valid.csv', mimeType: 'text/csv', buffer: Buffer.from('id,amount\n0012,1\n') },
    { name: 'unsupported.heic', mimeType: 'image/heic', buffer: Buffer.from('invalid') }
  ])
  await page.getByRole('alert').filter({ hasText: 'HEIC' }).waitFor()
  assert.equal(await page.getByTestId('chat-file-preview').count(), 0)
  assert.equal(requests.length, 3)
  const mobile = await context.newPage()
  await mobile.setViewportSize({ width: 390, height: 844 })
  mobile.on('pageerror', error => errors.push(error.message))
  await mobile.goto('http://127.0.0.1:4173/chat')
  await mobile.getByTestId('chat-input').waitFor()
  await mobile.locator('.chat-toolbar').getByRole('button', { name: /新对话/ }).click()
  assert.equal(await mobile.getByTestId('chat-camera-input').getAttribute('capture'), 'environment')
  assert.equal(await mobile.getByTestId('chat-image-input').getAttribute('capture'), null)
  await mobile.getByTestId('chat-camera-input').setInputFiles({ name: 'camera.jpg', mimeType: 'image/jpeg', buffer: Buffer.from(screenshotData.split(',')[1], 'base64') })
  await mobile.getByTestId('chat-image-preview').waitFor()
  await mobile.getByTestId('chat-image-input').setInputFiles({ name: 'gallery.png', mimeType: 'image/png', buffer: Buffer.from(screenshotData.split(',')[1], 'base64') })
  await mobile.getByTestId('chat-image-preview').nth(1).waitFor()
  assert.equal(requests.length, 3)
  await mobile.getByTestId('chat-send').click()
  await mobile.getByTestId('chat-download-excel').waitFor()
  assert.equal(requests.length, 4)
  assert.equal(requests[3].messages.at(-1).content.filter(part => part.type === 'image_url').length, 2)
  await mobile.reload()
  await mobile.locator('article img').nth(1).waitFor()
  await mobile.screenshot({ path: path.join(output, 'mobile-result.png') })
  const dimensions = await mobile.evaluate(() => ({ viewport: innerWidth, width: document.documentElement.scrollWidth,
    overflowing: Array.from(document.querySelectorAll('main *')).filter(element => element.getBoundingClientRect().right > innerWidth + 1)
      .slice(0, 10).map(element => ({ tag: element.tagName, classes: element.className })) }))
  assert.equal(dimensions.width <= dimensions.viewport, true, JSON.stringify(dimensions))
  assert.deepEqual(errors, [])
  await writeFile(path.join(output, 'result.json'), JSON.stringify({ passed: true, mock_model_requests: requests.length,
    real_model_requests: 0, csv_worker: true, xlsx_worker: true, xlsx_download: true, html_report: true,
    draft_restored: true, mobile_no_horizontal_overflow: true, browser_errors: errors,
    screenshot_paste: true, image_only_send: true, image_resize: true, image_followup: true, image_draft_restored: true,
    image_history_restored: true, legacy_history_migrated: true, camera_input: true, gallery_input: true,
    mixed_batch_atomic: true, native_camera_hardware_tested: false }, null, 2))
} finally { await browser.close() }
