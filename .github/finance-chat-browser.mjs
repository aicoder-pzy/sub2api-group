// Browser regression with synthetic financial data and intercepted APIs: no paid model calls.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { createServer } from 'node:http'

const require = createRequire(path.resolve(process.env.BROWSER_TEST_MODULES, 'probe.cjs'))
const { chromium } = require('playwright')
const XLSX = createRequire(path.resolve('frontend/package.json'))('xlsx')
const output = path.resolve('finance-browser-results')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const pendingStreams = new Map()
const streamLead = '已收到资料，正在核对。'
const modelServer = createServer((request, response) => {
  request.resume()
  response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'Access-Control-Allow-Origin': '*' })
  response.flushHeaders()
  const chunk = data => 'data: ' + JSON.stringify(data) + '\n\n'
  const delta = content => chunk({ choices: [{ index: 0, delta: { content }, finish_reason: null }] })
  const answer = '| 单号 | 差额 |\n| --- | --- |\n| 0012 | -120.50 |\n\n## 核对结论\n请按来源逐项复核。'
  const number = Number(request.headers['x-synthetic-request'])
  const finish = () => { response.end(delta(answer) + chunk({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] }) + 'data: [DONE]\n\n'); pendingStreams.delete(number) }
  if ([1, 4].includes(number)) {
    response.write(delta(streamLead + '\n\n'))
    pendingStreams.set(number, finish)
  } else finish()
})
await new Promise(resolve => modelServer.listen(0, '127.0.0.1', resolve))
const contrastResults = []
async function readable(theme, name, locator, pseudo = null) {
  await locator.waitFor({ state: 'visible' })
  const colors = await locator.evaluate(async (element, pseudo) => {
    await Promise.all(element.getAnimations().map(animation => animation.finished.catch(() => {})))
    const rgba = value => {
      const parts = value.match(/[\d.]+/g).map(Number)
      return [...parts.slice(0, 3), parts[3] ?? 1]
    }
    const over = (front, back) => [...front.slice(0, 3).map((value, i) => value * front[3] + back[i] * (1 - front[3])), 1]
    const ancestors = []
    for (let node = element; node; node = node.parentElement) ancestors.unshift(node)
    let background = [255, 255, 255, 1]
    for (const node of ancestors) background = over(rgba(getComputedStyle(node).backgroundColor), background)
    const style = getComputedStyle(element, pseudo), foreground = rgba(style.color)
    foreground[3] *= Number(style.opacity)
    const luminance = rgb => rgb.slice(0, 3).map(value => {
      const c = value / 255
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
    }).reduce((sum, value, i) => sum + value * [0.2126, 0.7152, 0.0722][i], 0)
    const a = luminance(over(foreground, background)), b = luminance(background)
    return { foreground: style.color, background: background.slice(0, 3), ratio: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) }
  }, pseudo)
  contrastResults.push({ theme, name, ...colors })
  await writeFile(path.join(output, 'contrast.json'), JSON.stringify(contrastResults, null, 2))
  if (colors.ratio < 4.5) await locator.page().screenshot({ path: path.join(output, `phone-${theme}-contrast-failure.png`) })
}
async function phoneLayout(page, minimum) {
  // Device metrics may update before resize events and Vue's next render have run.
  await page.waitForFunction(() => {
    const box = document.querySelector('.chat-shell').getBoundingClientRect(), viewport = visualViewport
    return Math.abs(box.height - viewport.height) < 1 && Math.abs(box.top - viewport.offsetTop) < 1
  })
  const dimensions = await page.evaluate(() => {
    const log = document.querySelector('[role="log"]'), send = document.querySelector('[data-testid="chat-send"]')
    const rect = send.getBoundingClientRect(), viewport = window.visualViewport
    return { viewport: innerWidth, width: document.documentElement.scrollWidth, page_scroll: window.scrollY,
      message_pane_height: log.clientHeight, send_top: rect.top, send_bottom: rect.bottom,
      visible_bottom: (viewport?.offsetTop || 0) + (viewport?.height || innerHeight) }
  })
  if (dimensions.width > dimensions.viewport || dimensions.message_pane_height < minimum || dimensions.send_bottom > dimensions.visible_bottom + 1) await page.screenshot({ path: path.join(output, 'phone-layout-failure.png') })
  assert.ok(dimensions.width <= dimensions.viewport, JSON.stringify(dimensions))
  assert.ok(dimensions.message_pane_height >= minimum, JSON.stringify(dimensions))
  assert.ok(dimensions.send_top >= 0 && dimensions.send_bottom <= dimensions.visible_bottom + 1, JSON.stringify(dimensions))
  assert.equal(dimensions.page_scroll, 0, JSON.stringify(dimensions))
  return dimensions
}
try {
  const context = await browser.newContext({ viewport: { width: 1366, height: 900 }, locale: 'zh-CN' })
  const user = { id: 1, role: 'user', email: 'finance@example.invalid', username: 'Finance', balance: 100, status: 'active', concurrency: 5 }
  const key = { id: 4, user_id: 1, name: 'Finance', key: 'TEST_KEY', group_id: 24, group: { id: 24, name: '财务办公' }, status: 'active', quota: 0, quota_used: 0 }
  const requests = [], errors = [], cloud = new Map()
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
  const handleRoute = async route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== 'http://127.0.0.1:4173') return route.abort()
    const json = data => route.fulfill({ json: { code: 0, data } })
    if (url.pathname === '/v1/models') return route.fulfill({ json: { data: [{ id: 'gpt-6.1-sol' }] } })
    if (url.pathname === '/v1/chat/completions') {
      assert.equal(request.headers().authorization, 'Bearer TEST_KEY')
      const payload = request.postDataJSON(); requests.push(payload)
      assert.equal(payload.messages[0].role, 'system')
      assert.equal(payload.stream, true)
      return route.continue({ url: `http://127.0.0.1:${modelServer.address().port}/v1/chat/completions`,
        headers: { ...request.headers(), 'x-synthetic-request': String(requests.length) } })
    }
    if (url.pathname.startsWith('/api/v1/chat/conversations')) {
      assert.equal(request.headers().authorization, 'Bearer TEST_PANEL_TOKEN')
      const id = url.pathname.split('/')[5]
      if (!id) return json([...cloud.values()].map(({ conversation, ...info }) => info))
      const existing = cloud.get(id)
      if (request.method() === 'GET') return existing ? json(existing) : route.fulfill({ status: 404, json: { code: 404 } })
      const input = request.postDataJSON()
      assert.ok(!JSON.stringify(input).includes('TEST_KEY'))
      if (input.revision !== (existing?.revision || 0) || existing?.deleted) return route.fulfill({ status: 409, json: { code: 409 } })
      const record = { id, revision: (existing?.revision || 0) + 1, deleted: input.deleted === true, conversation: input.conversation }
      cloud.set(id, record); return json(record)
    }
    if (url.pathname === '/api/v1/auth/me') return json(user)
    if (url.pathname === '/api/v1/keys') return json({ items: [key], total: 1, pages: 1, page: 1, page_size: 100 })
    if (url.pathname === '/api/v1/keys/4') return json(key)
    if (url.pathname === '/api/v1/settings/public') return json({ site_name: '财务办公', backend_mode_enabled: false, custom_menu_items: [] })
    if (url.pathname.includes('/setup/status')) return json({ needs_setup: false })
    if (url.pathname.startsWith('/api/')) return json([])
    return route.continue()
  }
  await context.route('**/*', handleRoute)
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
  await page.locator('.chat-markdown').filter({ hasText: streamLead }).waitFor()
  assert.equal(await page.getByTestId('chat-stop').isVisible(), true)
  assert.equal(await page.getByTestId('chat-download-excel').count(), 0, 'First text must render before stream completion')
  await page.screenshot({ path: path.join(output, 'desktop-stream-in-progress.png') })
  pendingStreams.get(1)()
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
  await page.waitForFunction(() => document.querySelector('[data-testid="chat-input"]')?.value === '刷新后继续核对')
  assert.match(await page.locator('.chat-markdown').textContent(), /0012/)
  assert.equal(requests.length, 1, 'Reload and exports must not call a model')
  // Generate an oversized-edge screenshot in Chromium; exercise the actual browser image decoder/canvas.
  const screenshotSources = await page.evaluate(() => {
    const canvas = document.createElement('canvas'); canvas.width = 3200; canvas.height = 1200
    const ctx = canvas.getContext('2d'); ctx.fillStyle = 'white'; ctx.fillRect(0, 0, 3200, 1200)
    ctx.fillStyle = '#111'; ctx.font = '90px sans-serif'; ctx.fillText('Receipt 0012   CNY -120.50', 100, 300)
    return { png: canvas.toDataURL('image/png'), jpeg: canvas.toDataURL('image/jpeg', 0.9) }
  })
  const screenshotData = screenshotSources.png
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
  await page.locator('form.chat-composer').evaluate((form, data) => {
    const bytes = Uint8Array.from(atob(data.split(',')[1]), char => char.charCodeAt(0))
    const transfer = new DataTransfer(); transfer.items.add(new File([bytes], 'dropped.png', { type: 'image/png' }))
    form.dispatchEvent(new DragEvent('drop', { dataTransfer: transfer, bubbles: true, cancelable: true }))
  }, screenshotData)
  await page.getByTestId('chat-image-preview').waitFor()
  await page.getByRole('button', { name: '移除文件 dropped.png', exact: true }).click()
  assert.equal(await page.getByTestId('chat-image-preview').count(), 0)
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
  await mobile.waitForFunction(() => !document.querySelector('[data-testid="chat-photo"]').disabled)
  await mobile.getByTestId('chat-mobile-history').click()
  await mobile.getByTestId('chat-mobile-history-dialog').getByRole('button', { name: /新对话/ }).click()
  await phoneLayout(mobile, 650)
  await mobile.getByTestId('chat-mobile-menu').click()
  await mobile.getByTestId('chat-mobile-model').waitFor({ state: 'visible' })
  assert.equal(await mobile.getByTestId('chat-mobile-model').inputValue(), 'gpt-6.1-sol')
  await mobile.screenshot({ path: path.join(output, 'mobile-settings.png') })
  await mobile.getByTestId('chat-mobile-settings-dialog').getByRole('button', { name: '关闭', exact: true }).click()
  await mobile.getByTestId('chat-mobile-plus').click()
  await mobile.getByTestId('chat-mobile-photo').waitFor({ state: 'visible' })
  await mobile.screenshot({ path: path.join(output, 'mobile-plus-tray.png') })
  await phoneLayout(mobile, 400)
  assert.equal(await mobile.getByTestId('chat-camera-input').getAttribute('capture'), 'environment')
  assert.equal(await mobile.getByTestId('chat-image-input').getAttribute('capture'), null)
  const cameraJPEG = Buffer.from(screenshotSources.jpeg.split(',')[1], 'base64')
  const orientationEXIF = Buffer.from('ffe1002245786966000049492a0008000000010012010300010000000600000000000000', 'hex')
  const cameraWithEXIF = Buffer.concat([cameraJPEG.subarray(0, 2), orientationEXIF, cameraJPEG.subarray(2)])
  const cameraChooser = mobile.waitForEvent('filechooser')
  await mobile.getByTestId('chat-mobile-camera').click()
  await (await cameraChooser).setFiles({ name: 'camera.jpg', mimeType: 'image/jpeg', buffer: cameraWithEXIF })
  await mobile.getByTestId('chat-image-preview').waitFor()
  assert.match(await mobile.getByTestId('chat-image-preview').textContent(), /960 × 2560/)
  const normalizedCamera = await mobile.getByTestId('chat-image-preview').locator('img').getAttribute('src')
  assert.equal(Buffer.from(normalizedCamera.split(',')[1], 'base64').includes(Buffer.from('Exif')), false)
  await mobile.getByTestId('chat-mobile-plus').click()
  const galleryChooser = mobile.waitForEvent('filechooser')
  await mobile.getByTestId('chat-mobile-photo').click()
  await (await galleryChooser).setFiles({ name: 'gallery.png', mimeType: 'image/png', buffer: Buffer.from(screenshotData.split(',')[1], 'base64') })
  await mobile.getByTestId('chat-image-preview').nth(1).waitFor()
  await phoneLayout(mobile, 550)
  assert.equal(requests.length, 3)
  await mobile.getByTestId('chat-send').click()
  await mobile.locator('.chat-markdown').filter({ hasText: streamLead }).waitFor()
  assert.equal(await mobile.getByTestId('chat-stop').isVisible(), true)
  assert.equal(await mobile.getByTestId('chat-download-excel').count(), 0)
  await mobile.screenshot({ path: path.join(output, 'mobile-stream-in-progress.png') })
  pendingStreams.get(4)()
  await mobile.getByTestId('chat-download-excel').waitFor()
  assert.equal(requests.length, 4)
  assert.equal(requests[3].messages.at(-1).content.filter(part => part.type === 'image_url').length, 2)
  await mobile.reload()
  await mobile.locator('article img').nth(1).waitFor()
  await mobile.screenshot({ path: path.join(output, 'mobile-result.png') })
  const dimensions = await phoneLayout(mobile, 650)
  // Independent browser storage, like a phone and a computer. Only the mock server is shared.
  // Finish the earlier same-browser camera scenario before testing a separate device.
  await mobile.close()
  await page.locator('aside.chat-history').getByRole('button', { name: /新对话/ }).click()
  await page.getByTestId('chat-sync').click()
  await page.getByTestId('chat-sync-status').filter({ hasText: '已同步' }).waitFor()
  await page.getByTestId('chat-share').click()
  await page.getByTestId('chat-share-qr').waitFor()
  assert.ok((await page.getByTestId('chat-share-qr').getAttribute('src')).startsWith('data:image/png;base64,'))
  const sharedURL = page.url()
  const phoneContext = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, locale: 'zh-CN' })
  await phoneContext.addInitScript(user => {
    localStorage.setItem('auth_token', 'TEST_PANEL_TOKEN'); localStorage.setItem('auth_user', JSON.stringify(user)); localStorage.setItem('sub2api_locale', 'zh')
  }, user)
  await phoneContext.route('**/*', handleRoute)
  const phone = await phoneContext.newPage(); phone.on('pageerror', error => errors.push(error.message))
  await phone.goto(sharedURL)
  await phone.getByTestId('chat-sync-status').filter({ hasText: '已同步' }).waitFor()
  await phone.waitForFunction(() => !document.querySelector('[data-testid="chat-photo"]').disabled)
  assert.equal(await phone.getByTestId('chat-input').inputValue(), await page.getByTestId('chat-input').inputValue())
  assert.equal(await phone.locator('article img').count(), await page.locator('article img').count())
  await page.getByRole('button', { name: '关闭预览', exact: true }).last().click()
  await page.getByTestId('chat-input').fill('电脑继续核对这张手机票据')
  await phone.getByTestId('chat-camera-input').setInputFiles({ name: 'phone-shared.jpg', mimeType: 'image/jpeg', buffer: cameraJPEG })
  await phone.getByRole('button', { name: '放大查看 phone-shared.jpg', exact: true }).waitFor()
  await phone.getByTestId('chat-sync-status').filter({ hasText: '已同步' }).waitFor()
  await page.getByRole('button', { name: '放大查看 phone-shared.jpg', exact: true }).waitFor({ timeout: 15000 })
  assert.equal(await page.getByTestId('chat-input').inputValue(), '电脑继续核对这张手机票据')
  assert.equal(requests.length, 4, 'Phone photo transfer must not call a model')
  await page.screenshot({ path: path.join(output, 'desktop-phone-photo.png') })
  await phone.screenshot({ path: path.join(output, 'phone-shared-conversation.png') })
  await page.getByTestId('chat-send').click()
  await page.getByTestId('chat-download-excel').waitFor()
  await phone.getByTestId('chat-download-excel').waitFor({ timeout: 15000 })
  assert.equal(await phone.getByTestId('chat-image-preview').count(), 0, 'Sent photo must not remain in the phone draft')
  assert.equal(requests.length, 5)
  await phone.screenshot({ path: path.join(output, 'phone-shared-reply.png') })
  // Long history must scroll inside the message pane, with the input always reachable.
  const sharedId = new URL(sharedURL).searchParams.get('conversation'), record = cloud.get(sharedId)
  record.conversation.messages.push(...Array.from({ length: 30 }, (_, index) => ({ id: 'history-' + index,
    role: index % 2 ? 'assistant' : 'user', content: '票据核对记录 ' + index + '\n请保留单号与金额用于复核。', status: 'complete', reasoning: '', attachments: [], images: [] })))
  Object.assign(record.conversation.messages.at(-1), { content: '票据核对记录 29\n\n| 单号 | 金额 |\n| --- | --- |\n| 0012 | -120.50 |\n\n> 请复核原始凭证\n\n[凭证说明](https://example.invalid)与 `0012`。', reasoning: '依据原始凭证核对金额。' })
  record.revision++
  await phone.getByTestId('chat-sync').click()
  await phone.getByText('票据核对记录 29', { exact: false }).waitFor()
  await phone.locator('[role="log"]').evaluate(log => { log.scrollTop = 0 })
  await phone.locator('[role="log"]').hover()
  await phone.mouse.wheel(0, 300)
  await phone.waitForFunction(() => document.querySelector('[role="log"]').scrollTop > 0)
  await phoneLayout(phone, 650)
  await phone.getByTestId('chat-input').fill('手机回车只换行')
  await phone.getByTestId('chat-input').press('End')
  await phone.getByTestId('chat-input').press('Enter')
  assert.equal(await phone.getByTestId('chat-input').inputValue(), '手机回车只换行\n')
  assert.equal(requests.length, 5)
  await phone.getByTestId('chat-input').fill('')
  await phone.setViewportSize({ width: 320, height: 568 })
  await phoneLayout(phone, 400)
  await phone.screenshot({ path: path.join(output, 'phone-small-320.png') })
  await phone.setViewportSize({ width: 390, height: 844 })
  // Keyboard-like visual viewport reduction, without changing the layout viewport (Safari behavior).
  await phone.evaluate(() => {
    Object.defineProperty(visualViewport, 'height', { configurable: true, value: 400 })
    Object.defineProperty(visualViewport, 'offsetTop', { configurable: true, value: 24 })
    visualViewport.dispatchEvent(new Event('resize'))
  })
  await phone.getByTestId('chat-input').fill('长草稿\n'.repeat(30))
  await phoneLayout(phone, 180)
  await phone.screenshot({ path: path.join(output, 'phone-keyboard-viewport.png') })
  await phone.evaluate(() => {
    delete visualViewport.height; delete visualViewport.offsetTop
    visualViewport.dispatchEvent(new Event('resize'))
  })
  await phone.getByTestId('chat-input').fill('')
  await phone.getByTestId('chat-image-input').setInputFiles(Array.from({ length: 4 }, (_, index) => ({ name: 'receipt-' + index + '.jpg', mimeType: 'image/jpeg', buffer: cameraJPEG })))
  await phone.getByTestId('chat-image-preview').nth(3).waitFor()
  await phoneLayout(phone, 550)
  await phone.screenshot({ path: path.join(output, 'phone-four-photo-draft.png') })
  assert.equal(requests.length, 5)
  for (let index = 0; index < 4; index++) await phone.getByRole('button', { name: '移除文件 receipt-' + index + '.jpg', exact: true }).click()
  await phone.getByTestId('chat-sync').click()
  await phone.getByTestId('chat-sync-status').filter({ hasText: '已同步' }).waitFor()
  assert.equal(cloud.get(sharedId).conversation.draftImages.length, 0)
  // Verify saved light preference and automatic OS dark preference through the real app boot.
  for (const theme of ['light', 'dark']) {
    await phone.emulateMedia({ colorScheme: theme })
    await phone.evaluate(theme => theme === 'light' ? localStorage.setItem('theme', theme) : localStorage.removeItem('theme'), theme)
    await phone.reload()
    await phone.locator('.chat-markdown table').last().waitFor()
    assert.equal(await phone.locator('html').evaluate(html => html.classList.contains('dark')), theme === 'dark')
    await phoneLayout(phone, 650)
    await phone.locator('.chat-assistant details summary').last().click()
    for (const [name, selector] of [
      ['header', '.chat-mobile-header h1'], ['sync status', '.chat-sync-bar > span'], ['sync refresh', '.chat-sync-bar > button'],
      ['user message', '.chat-user-bubble'], ['assistant body', '.chat-markdown'], ['assistant table', '.chat-markdown td'],
      ['assistant sender', '.chat-assistant .chat-sender'], ['user sender', '.chat-user .chat-sender'],
      ['reasoning label', '.chat-assistant details summary'], ['reasoning body', '.chat-assistant details p'],
      ['blockquote', '.chat-markdown blockquote'], ['link', '.chat-markdown a'], ['inline code', '.chat-markdown code'],
      ['assistant action', '[data-testid="chat-copy-table"]'], ['input text', '[data-testid="chat-input"]']
    ]) await readable(theme, name, phone.locator(selector).last())
    await readable(theme, 'input placeholder', phone.getByTestId('chat-input'), '::placeholder')
    assert.equal(await phone.getByTestId('chat-send').isDisabled(), true)
    await readable(theme, 'disabled send', phone.getByTestId('chat-send'))
    await phone.getByTestId('chat-input').fill('核对单号 0012 和金额 -120.50')
    assert.equal(await phone.getByTestId('chat-send').isDisabled(), false)
    await readable(theme, 'enabled send', phone.getByTestId('chat-send'))
    await phone.locator('.chat-messages').evaluate(log => { log.scrollTop = log.scrollHeight })
    await phone.screenshot({ path: path.join(output, `phone-${theme}-conversation.png`) })
    await phone.getByTestId('chat-mobile-plus').click()
    await readable(theme, 'camera label', phone.getByTestId('chat-mobile-camera'))
    await readable(theme, 'tray hint', phone.locator('.chat-mobile-tray p'))
    await readable(theme, 'finance task', phone.locator('.chat-mobile-tray .grid').nth(1).locator('button').first())
    await phone.screenshot({ path: path.join(output, `phone-${theme}-tray.png`) })
    await phone.getByTestId('chat-mobile-menu').click()
    const settings = phone.getByTestId('chat-mobile-settings-dialog')
    for (const [name, selector] of [['settings heading', 'h2'], ['settings label', '.chat-field > span'], ['settings hint', 'p'],
      ['settings input', '.input'], ['settings action', '.btn-secondary'], ['delete action', '.text-red-600']]) {
      await readable(theme, name, settings.locator(selector).first())
    }
    await readable(theme, 'model placeholder', phone.getByTestId('chat-mobile-model'), '::placeholder')
    await phone.screenshot({ path: path.join(output, `phone-${theme}-settings.png`) })
    await settings.locator('.chat-mobile-button').click()
    await phone.getByTestId('chat-mobile-history').click()
    const history = phone.getByTestId('chat-mobile-history-dialog')
    await readable(theme, 'new conversation', history.locator('.btn-primary'))
    await readable(theme, 'history item', history.locator('.chat-history-item').first())
    await readable(theme, 'history search', history.locator('input'), '::placeholder')
    await readable(theme, 'dashboard link', history.locator('a'))
    await phone.screenshot({ path: path.join(output, `phone-${theme}-history.png`) })
    await history.locator('.chat-mobile-button').click()
    await phone.getByTestId('chat-input').fill('')
    await phone.getByTestId('chat-sync').click()
    await phone.getByTestId('chat-sync-status').filter({ hasText: '已同步' }).waitFor()
  }
  assert.equal(requests.length, 5, 'Theme verification must not call a model')
  assert.deepEqual(contrastResults.filter(result => result.ratio < 4.5), [], 'All mobile text must meet 4.5:1 contrast')
  await phoneContext.close()
  assert.deepEqual(errors, [])
  await writeFile(path.join(output, 'result.json'), JSON.stringify({ passed: true, mock_model_requests: requests.length,
    real_model_requests: 0, csv_worker: true, xlsx_worker: true, xlsx_download: true, html_report: true,
    two_independent_devices: true, phone_photo_transfer_without_inference: true, shared_reply: true, authenticated_sync: true, draft_restored: true, mobile_no_horizontal_overflow: true, browser_errors: errors,
    screenshot_paste: true, image_only_send: true, image_resize: true, image_followup: true, image_draft_restored: true,
    image_history_restored: true, legacy_history_migrated: true, camera_input: true, gallery_input: true,
    mixed_batch_atomic: true, image_drop: true, camera_orientation: true, camera_exif_removed: true,
    mobile_message_pane_height: dimensions.message_pane_height, mobile_history_menu: true, mobile_settings_sheet: true,
    mobile_plus_tray: true, mobile_keyboard_viewport: true, mobile_small_screen: true, mobile_internal_scroll: true,
    mobile_four_photo_draft: true, mobile_enter_newline: true, mobile_light_and_dark: true, mobile_minimum_text_contrast: Math.min(...contrastResults.map(result => result.ratio)),
    mobile_contrast_checks: contrastResults.length, automatic_dark_theme: true, incremental_http_stream_desktop: true,
    incremental_http_stream_mobile: true, native_camera_hardware_tested: false }, null, 2))
} finally { await browser.close(); modelServer.closeAllConnections(); await new Promise(resolve => modelServer.close(resolve)) }
