import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'

const { chromium } = createRequire(path.resolve(process.env.BROWSER_TEST_MODULES, 'package.json'))('playwright')
const output = path.resolve('zako-feature-browser-results')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const errors = [], networkFailures = [], imports = [], creations = [], policies = []
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, locale: 'zh-CN' })
  const user = { id: 1, role: 'admin', email: 'admin@example.invalid', username: 'Admin', balance: 100, status: 'active', concurrency: 5 }
  const group = { id: 1, name: 'Test OpenAI', platform: 'openai', status: 'active', subscription_type: 'standard', rate_multiplier: 1, is_exclusive: false }
  const key = { id: 1, name: 'Test Key', user_id: 1, key: 'sk-browser-fixture', group_id: 1, group, status: 'active', quota: 0, quota_used: 0, rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0, ip_whitelist: [], ip_blacklist: [], model_mapping: {}, prompt_policy: {}, created_at: '2026-11-04T00:00:00Z' }
  const site = { id: '12345678-1234-4234-8234-123456789012', name: 'Test upstream', kind: 'newapi', base_url: 'https://upstream.example.invalid', user_id: 7, proxy_id: null, revision: 1 }
  await context.addInitScript(user => {
    localStorage.setItem('auth_token', 'TEST_PANEL_TOKEN')
    localStorage.setItem('auth_user', JSON.stringify(user))
    localStorage.setItem('sub2api_locale', 'zh')
    localStorage.setItem('admin_guide_1_admin_v4_interactive', 'true')
  }, user)
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), p = url.pathname
    if (url.origin !== 'http://127.0.0.1:4173') return route.abort('blockedbyclient')
    if (p.startsWith('/assets/')) return route.fulfill({ path: path.resolve('backend/internal/web/dist', p.slice(1)) })
    const json = data => route.fulfill({ json: { code: 0, data } })
    if (p === '/api/v1/auth/me') return json(user)
    if (p === '/api/v1/admin/compliance') return json({ required: false })
    if (p === '/api/v1/settings/public') return json({ site_name: 'Sub2API', backend_mode_enabled: false, custom_menu_items: [] })
    if (p.includes('/setup/status')) return json({ needs_setup: false })
    if (p === '/api/v1/keys/prompt-profiles') return json([{ id: 'zako-default', name: 'Zako · 通用默认策略', models: [], bytes: 3844 }, { id: 'test-model-profile', name: 'Test model strategy', models: ['model-1'], bytes: 2048 }])
    if (p === '/api/v1/keys/1' && request.method() === 'PUT') { const payload = request.postDataJSON(); policies.push(payload); Object.assign(key, payload); return json(key) }
    if (p === '/api/v1/keys') return json({ items: [key], total: 1, page: 1, page_size: 20, pages: 1 })
    if (p === '/api/v1/usage/dashboard/api-keys-usage') return json({ stats: {} })
    if (p.includes('/groups') && !p.includes('upstream-sites')) return json([group])
    if (p === '/api/v1/admin/accounts') return json({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    const sites = '/api/v1/admin/accounts/upstream-sites'
    if (p === sites) return json([site])
    if (p === `${sites}/${site.id}/inventory`) return json({ revision: 1, groups: [{ id: 'vip', name: 'VIP', multiplier: 0.5 }], keys: [{ id: '3', name: 'upstream-key', group_id: 'vip', group_name: 'VIP', available: true }] })
    if (p.endsWith('/keys/3/models')) return json(['model-1', 'model-2'])
    if (p.endsWith('/import')) { imports.push(request.postDataJSON()); return json({ account_id: 100, name: 'Imported' }) }
    if (p.endsWith(`/${site.id}/keys`) && request.method() === 'POST') { creations.push(request.postDataJSON()); return json({ created: true }) }
    if (p.startsWith('/api/')) return json([])
    return route.continue()
  })
  let page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.type() === 'error' && !message.text().includes('net::ERR_BLOCKED_BY_CLIENT')) errors.push(message.text()) })
  context.on('requestfailed', request => networkFailures.push({ url: request.url(), error: request.failure()?.errorText }))
  await page.goto('http://127.0.0.1:4173/admin/accounts')
  await page.getByRole('button', { name: '上游站点与密钥导入', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '上游站点与密钥导入', exact: true })
  await dialog.getByRole('combobox', { name: '选择已保存站点' }).selectOption(site.id)
  await dialog.getByRole('button', { name: '读取密钥与上游分组', exact: true }).click()
  await dialog.getByLabel('选择上游密钥', { exact: true }).selectOption('3')
  await dialog.getByRole('button', { name: '拉取可用模型', exact: true }).click()
  await dialog.getByRole('checkbox', { name: 'model-1', exact: true }).check()
  await dialog.getByLabel('导入到本地分组', { exact: true }).selectOption('1')
  await dialog.getByRole('button', { name: '导入选中密钥', exact: true }).click()
  await dialog.getByText('账号导入成功', { exact: true }).waitFor()
  assert.equal(imports.length, 1)
  assert.deepEqual(imports[0].models, ['model-1'])
  assert.equal(imports[0].group_id, 1)
  assert.equal('api_key' in imports[0], false)
  assert.equal(creations.length, 0)
  await dialog.getByLabel('上游分组', { exact: true }).selectOption('vip')
  await dialog.getByRole('checkbox', { name: '我确认在上游创建这把密钥', exact: true }).check()
  await dialog.getByRole('button', { name: '在上游创建密钥', exact: true }).click()
  await dialog.getByText('已提交创建，请刷新列表确认', { exact: true }).waitFor()
  assert.equal(creations.length, 1)
  assert.equal(creations[0].confirmed, true)
  for (const width of [1280, 375, 320]) {
    await page.setViewportSize({ width, height: 900 })
    await page.screenshot({ path: path.join(output, `sites-${width}.png`) })
    assert.equal(await dialog.locator('.modal-content').evaluate(element => element.scrollWidth <= element.clientWidth + 2), true)
  }
  await dialog.getByRole('button', { name: 'Close modal', exact: true }).click()
  await page.close()
  page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.type() === 'error' && !message.text().includes('net::ERR_BLOCKED_BY_CLIENT')) errors.push(message.text()) })
  await page.goto('http://127.0.0.1:4173/keys')
  await page.getByRole('button', { name: '编辑', exact: true }).first().click()
  const editor = page.getByRole('dialog').filter({ has: page.getByText('提示词策略', { exact: true }) })
  await editor.getByRole('button', { name: '添加模型策略', exact: true }).click()
  await editor.getByRole('combobox', { name: '选择内置策略', exact: true }).selectOption('zako-default')
  assert.equal(await editor.getByRole('textbox', { name: '模型名，或 * 作为默认策略', exact: true }).inputValue(), '*')
  await page.screenshot({ path: path.join(output, 'key-prompt-editor.png') })
  await editor.getByRole('button', { name: /保存|更新/ }).last().click()
  await page.waitForFunction(() => !document.querySelector('[role=dialog]'))
  assert.equal(policies.length, 1)
  assert.deepEqual(policies[0].prompt_policy, { '*': 'zako-default' })
  assert.equal(errors.length, 0, errors.join('\n'))
  await writeFile(path.join(output, 'result.json'), JSON.stringify({ errors, networkFailures, imports, creations, policies, realRequests: 0 }, null, 2))
} catch (error) {
  for (const page of browser.contexts().flatMap(context => context.pages())) {
    await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {})
    await writeFile(path.join(output, 'failure.html'), await page.content()).catch(() => {})
  }
  await writeFile(path.join(output, 'errors.json'), JSON.stringify({ errors, networkFailures, error: String(error) }, null, 2))
  throw error
} finally { await browser.close() }
