import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'

const require = createRequire(path.resolve(process.env.BROWSER_TEST_MODULES, 'package.json'))
const { chromium } = require('playwright')
const XLSX = createRequire(path.resolve('frontend/package.json'))('xlsx')
const output = path.resolve('email-broadcast-browser-results')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const errors = [], jobs = [], creates = [], tests = [], cancellations = []
try {
  const context = await browser.newContext({ viewport: { width: 1366, height: 1000 }, locale: 'zh-CN' })
  const user = { id: 1, role: 'admin', email: 'admin@example.invalid', username: 'Admin', balance: 100, status: 'active', concurrency: 5 }
  await context.addInitScript(user => {
    localStorage.setItem('auth_token', 'TEST_PANEL_TOKEN')
    localStorage.setItem('auth_user', JSON.stringify(user))
    localStorage.setItem('sub2api_locale', 'zh')
  }, user)
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), pathname = url.pathname
    if (url.origin !== 'http://127.0.0.1:4173') return route.abort()
    const json = data => route.fulfill({ json: { code: 0, data } })
    if (pathname === '/api/v1/auth/me') return json(user)
    if (pathname === '/api/v1/admin/compliance') return json({ required: false, version: 'test', ack_phrase_zh: '', ack_phrase_en: '' })
    if (pathname === '/api/v1/settings/public') return json({ site_name: 'Sub2API', backend_mode_enabled: false, custom_menu_items: [] })
    if (pathname.includes('/setup/status')) return json({ needs_setup: false })
    const root = '/api/v1/admin/email-broadcasts'
    if (pathname.startsWith(root)) {
      assert.equal(request.headers().authorization, 'Bearer TEST_PANEL_TOKEN')
      if (pathname === root + '/preview') {
        const tokens = request.postDataJSON().recipients.split(/[\s,;，；、]+/).filter(Boolean)
        const converted = tokens.map(value => /^\d{5,12}$/.test(value) ? value + '@qq.com' : value.toLowerCase())
        const valid = converted.filter(value => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(value))
        return json({ recipients: [...new Set(valid)], invalid: converted.filter(value => !valid.includes(value)), duplicates: valid.length - new Set(valid).size })
      }
      if (pathname === root + '/test') { tests.push(request.postDataJSON()); return json({ sent: true }) }
      if (pathname.endsWith('/cancel')) {
        cancellations.push(pathname)
        const job = jobs.find(job => pathname.includes(job.broadcast.id))
        job.broadcast.status = 'cancelled'; job.broadcast.cancelled = job.broadcast.pending; job.broadcast.pending = 0
        job.recipients.forEach(recipient => { recipient.status = 'cancelled' })
        return json({ cancelled: true })
      }
      if (pathname === root && request.method() === 'POST') {
        const input = request.postDataJSON(); creates.push(input)
        const recipients = input.recipients.split('\n').map(email => ({ email, status: 'pending', error: '' }))
        const job = { broadcast: { ...input, created_at: new Date().toISOString(), status: 'queued', total: recipients.length, pending: recipients.length, sending: 0, sent: 0, failed: 0, unknown: 0, cancelled: 0 }, recipients, total: recipients.length }
        jobs.push(job); return json(job)
      }
      if (pathname === root) return json({ items: jobs.map(job => job.broadcast) })
      return json(jobs.find(job => pathname.endsWith(job.broadcast.id)))
    }
    if (pathname.startsWith('/api/')) return json([])
    return route.continue()
  })
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('http://127.0.0.1:4173/admin/email-broadcasts')
  try {
    await page.getByTestId('email-broadcast-title').waitFor({ timeout: 10000 })
  } catch (error) {
    await page.screenshot({ path: path.join(output, 'route-load-failure.png'), fullPage: true })
    console.error(JSON.stringify({ url: page.url(), title: await page.title(), body: (await page.locator('body').innerText()).slice(0, 2000), errors }))
    throw error
  }
  const book = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(book, XLSX.utils.aoa_to_sheet([['QQ号', 'ignore'], [123456789, 11111111], ['123456789@qq.com'], ['user@example.com'], ['错误项']]), 'Contacts')
  await page.getByTestId('broadcast-file').setInputFiles({ name: 'qq.xlsx', mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', buffer: XLSX.write(book, { type: 'buffer', bookType: 'xlsx' }) })
  await page.getByText('有效 2 位 · 已去重 1 条 · 无效 1 条', { exact: true }).waitFor()
  assert.equal(creates.length, 0)
  assert.equal(tests.length, 0)
  await page.getByRole('button', { name: '仅保留有效地址', exact: true }).click()
  assert.equal(await page.locator('#broadcast-recipients').inputValue(), '123456789@qq.com\nuser@example.com')
  await page.locator('#broadcast-subject').fill('模型上线通知')
  await page.locator('#broadcast-content').fill('第一行\n<script>window.UNSAFE = 1</script>\n第二行')
  await page.locator('#broadcast-test-recipient').fill('987654321')
  await page.getByRole('button', { name: '发送一封测试邮件', exact: true }).click()
  await page.getByText('测试邮件已提交，请到收件箱或垃圾邮件中检查。', { exact: true }).waitFor()
  assert.equal(tests.length, 1)
  assert.equal(creates.length, 0)
  await page.getByRole('button', { name: '预览并确认群发', exact: true }).click()
  await page.getByTestId('broadcast-confirm').waitFor()
  assert.equal(creates.length, 0)
  assert.equal(await page.evaluate(() => window.UNSAFE), undefined)
  await page.screenshot({ path: path.join(output, 'desktop-review.png'), fullPage: true })
  await page.getByTestId('broadcast-confirm').click()
  await page.getByTestId('broadcast-detail').waitFor()
  assert.equal(creates.length, 1)
  assert.equal(creates[0].interval_seconds, 2)
  assert.equal(creates[0].recipients, '123456789@qq.com\nuser@example.com')
  await page.reload()
  await page.getByRole('button', { name: '停止后续发送', exact: true }).waitFor()
  await page.getByRole('button', { name: '停止后续发送', exact: true }).click()
  await page.getByText('已停止后续发送，正在提交的一封仍可能完成。', { exact: true }).waitFor()
  assert.equal(cancellations.length, 1)
  await page.screenshot({ path: path.join(output, 'desktop-history.png'), fullPage: true })
  for (const theme of ['light', 'dark']) {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.waitForFunction(() => {
      const sidebar = document.querySelector('.sidebar')?.getBoundingClientRect()
      const main = document.querySelector('main')?.getBoundingClientRect()
      return sidebar && main && sidebar.right <= 1 && main.left <= 1
    })
    await page.evaluate(theme => {
      document.documentElement.classList.toggle('dark', theme === 'dark')
      document.documentElement.dataset.theme = theme
    }, theme)
    await page.locator('#broadcast-recipients').fill('123456789\n987654321')
    const dimensions = await page.evaluate(() => {
      const overflow = [...document.querySelectorAll('body *')]
        .map(element => {
          const rect = element.getBoundingClientRect()
          return {
            tag: element.tagName.toLowerCase(),
            id: element.id,
            className: typeof element.className === 'string' ? element.className : '',
            left: Math.round(rect.left),
            right: Math.round(rect.right)
          }
        })
        .filter(element => element.right > innerWidth + 1)
        .sort((a, b) => b.right - a.right)
        .slice(0, 12)
      return { viewport: innerWidth, width: document.documentElement.scrollWidth, overflow }
    })
    if (dimensions.width > dimensions.viewport) await page.screenshot({ path: path.join(output, `phone-${theme}-overflow.png`), fullPage: true })
    assert.ok(dimensions.width <= dimensions.viewport, JSON.stringify(dimensions))
    await page.screenshot({ path: path.join(output, `phone-${theme}.png`), fullPage: true })
  }
  assert.deepEqual(errors, [])
  await writeFile(path.join(output, 'result.json'), JSON.stringify({ passed: true, mock_broadcasts: creates.length, mock_tests: tests.length, mock_cancellations: cancellations.length, real_emails_sent: 0, console_errors: errors }, null, 2))
} finally { await browser.close() }
