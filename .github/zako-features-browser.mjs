import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'

const { chromium } = createRequire(path.resolve(process.env.BROWSER_TEST_MODULES, 'package.json'))('playwright')
const output = path.resolve('zako-feature-browser-results')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const errors = [], networkFailures = [], imports = [], creations = [], policies = [], modelWrites = [], links = [], groupChanges = []
const importedAccounts = []
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, locale: 'zh-CN' })
  const user = { id: 1, role: 'admin', email: 'admin@example.invalid', username: 'Admin', balance: 100, status: 'active', concurrency: 5 }
  const group = { id: 1, name: 'Test OpenAI', platform: 'openai', status: 'active', subscription_type: 'standard', rate_multiplier: 1, is_exclusive: false }
  const key = { id: 1, name: 'Test Key', user_id: 1, key: 'sk-browser-fixture', group_id: 1, group, status: 'active', quota: 0, quota_used: 0, rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0, ip_whitelist: [], ip_blacklist: [], model_mapping: {}, prompt_policy: {}, created_at: '2026-11-04T00:00:00Z' }
  const site = { id: '12345678-1234-4234-8234-123456789012', name: 'Test upstream', kind: 'newapi', base_url: 'https://upstream.example.invalid', user_id: 7, proxy_id: null, revision: 1, inference_url: 'https://relay.example.invalid', purchase_note: 'Browser fixture', account_count: 0, catalogs: {} }
  const inventory = { revision: 1, status: 'ok', attempt_at: '2026-11-07T01:00:00Z', received_at: '2026-11-07T01:00:00Z', wallet_at: '2026-11-07T01:00:00Z', wallet: { currency: 'QUOTA', remaining: 1234567, unlimited: false }, errors: {}, groups: [{ id: 'vip', name: 'VIP', multiplier: 0.5 }], keys: [{ id: '3', name: '主用 Key', group_id: 'vip', group_name: 'VIP', available: true, status: 'active', suffix: '3abc', quota: { currency: 'QUOTA', unlimited: true } }, { id: '4', name: '备用 Key', group_id: 'vip', group_name: 'VIP', available: true, status: 'active', suffix: '4abc' }, { id: '9', name: '额度耗尽 Key', group_id: 'vip', group_name: 'VIP', available: false, status: 'quota_exhausted', quota: { currency: 'QUOTA', remaining: 0, unlimited: false } }] }
  site.inventory = inventory
  inventory.groups[0].description = '当前分组的完整介绍'
  inventory.groups.push({ id: 'fast', name: '快速分组', multiplier: 0.15, description: '支持完整上下文与缓存。\n第二行：完整介绍不截断。<script>window.upstreamInjected = true</script>' })
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
    if (p === `${sites}/${site.id}/inventory`) return json(inventory)
    if (p === `${sites}/${site.id}/accounts`) return json(importedAccounts)
    if (p === `${sites}/${site.id}/keys/3/groups`) return json({ key: inventory.keys[0], groups: inventory.groups })
    if (p === `${sites}/${site.id}/keys/3/group` && request.method() === 'PUT') {
      const payload = request.postDataJSON(); groupChanges.push(payload)
      assert.equal(payload.current_group_id, inventory.keys[0].group_id)
      Object.assign(inventory.keys[0], { group_id: payload.group_id, group_name: '快速分组' })
      return json(inventory.keys[0])
    }
    if (/\/keys\/[34]\/models$/.test(p)) return json(p.includes('/keys/3/') ? ['model-1', 'model-2'] : ['model-2'])
    if (p.endsWith('/import')) {
      const payload = request.postDataJSON(), identity = request.headers()['idempotency-key']
      imports.push({ payload, identity })
      if (payload.key_id === '4' && imports.filter(item => item.payload.key_id === '4').length === 1) return route.fulfill({ json: { code: 400, message: 'Fixture temporary failure' } })
      const account = { id: payload.key_id === '3' ? 100 : 101, key_id: payload.key_id, name: payload.name, platform: payload.platform, status: 'active', source: 'imported', models: Object.fromEntries(payload.models.map(model => [model, model])), concurrency: payload.concurrency, rate_multiplier: payload.rate_multiplier }
      if (!importedAccounts.some(item => item.id === account.id)) importedAccounts.push(account)
      return json({ account_id: account.id, name: account.name })
    }
    if (/\/accounts\/100\/models$/.test(p)) {
      const payload = request.postDataJSON(); modelWrites.push(payload)
      return json({ account_id: 100, version: 'fixture-preview-version', added: ['model-3'], removed: [], mapping: { 'model-1': 'model-1', 'model-3': 'model-3' } })
    }
    if (p.endsWith('/accounts/link')) { links.push(request.postDataJSON()); return json({ saved: true }) }
    if (p.endsWith(`/${site.id}/keys`) && request.method() === 'POST') { creations.push(request.postDataJSON()); return json({ created: true }) }
    if (p.startsWith('/api/')) return json([])
    return route.continue()
  })
  let page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.type() === 'error' && !message.text().includes('net::ERR_BLOCKED_BY_CLIENT')) errors.push(message.text()) })
  context.on('requestfailed', request => networkFailures.push({ url: request.url(), error: request.failure()?.errorText }))
  await page.goto('http://127.0.0.1:4173/admin/accounts')
  await page.getByRole('link', { name: '上游站点', exact: true }).last().click()
  await page.getByRole('main').getByRole('heading', { name: '上游站点', exact: true }).waitFor()
  await page.getByRole('checkbox', { name: '选择 Key：主用 Key', exact: true }).check()
  await page.getByRole('checkbox', { name: '选择 Key：备用 Key', exact: true }).check()
  assert.equal(await page.getByRole('checkbox', { name: '选择 Key：额度耗尽 Key', exact: true }).isDisabled(), true)
  assert.equal(await page.locator('[data-site-wallet]').count(), 1)
  assert.match(await page.locator('[data-site-wallet]').textContent(), /配额单位/)
  await page.screenshot({ path: path.join(output, 'sites-desktop.png') })
  await page.getByRole('button', { name: '批量导入', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: '批量导入账号', exact: true })
  await drawer.getByLabel('导入到本地分组').selectOption('1')
  await drawer.getByRole('button', { name: '读取模型并预览', exact: true }).click()
  await drawer.getByLabel('model-1', { exact: true }).waitFor()
  await drawer.locator('[data-import-key="3"]').getByLabel('model-2', { exact: true }).uncheck()
  await page.screenshot({ path: path.join(output, 'batch-import-desktop.png') })
  await drawer.getByRole('button', { name: '确认导入', exact: true }).click()
  await drawer.getByText('Fixture temporary failure', { exact: false }).waitFor()
  assert.equal(imports.length, 2)
  assert.deepEqual(imports[0].payload.models, ['model-1'])
  assert.deepEqual(imports[1].payload.models, ['model-2'])
  assert.equal(imports[0].payload.group_id, 1)
  assert.equal('api_key' in imports[0].payload, false)
  assert.equal(creations.length, 0)
  await drawer.getByRole('button', { name: '重试失败与未完成项', exact: true }).click()
  await drawer.getByText('已完成 2 / 2 项', { exact: true }).waitFor()
  assert.equal(imports.length, 3)
  assert.equal(imports[1].identity, imports[2].identity)
  assert.deepEqual(imports[1].payload, imports[2].payload)
  assert.equal(imports.filter(item => item.payload.key_id === '3').length, 1)
  for (const width of [375, 320]) {
    await page.setViewportSize({ width, height: 900 })
    await page.screenshot({ path: path.join(output, `batch-import-${width}.png`) })
    assert.equal(await drawer.locator('.modal-content').evaluate(element => element.scrollWidth <= element.clientWidth + 2), true)
  }
  await drawer.getByRole('button', { name: '关闭', exact: true }).click()
  for (const width of [1440, 375, 320]) {
    await page.setViewportSize({ width, height: 1000 })
    // The shared app shell animates its sidebar and main margin across breakpoints.
    await page.waitForFunction(() => document.documentElement.scrollWidth <= innerWidth + 2)
    await page.screenshot({ path: path.join(output, `sites-${width}.png`), animations: 'disabled' })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 2), true, `Page overflow at ${width}`)
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.getByRole('tab', { name: '模型变化', exact: true }).click()
  await page.getByRole('button', { name: '预览模型变化', exact: true }).first().click()
  await page.getByRole('button', { name: '确认应用变化', exact: true }).waitFor()
  assert.equal(modelWrites.length, 1)
  assert.equal(modelWrites[0].apply, false)
  await page.screenshot({ path: path.join(output, 'model-changes.png') })
  await page.getByRole('button', { name: '确认应用变化', exact: true }).click()
  await page.getByText('模型范围已更新', { exact: true }).waitFor()
  assert.equal(modelWrites.length, 2)
  assert.equal(modelWrites[1].preview.version, 'fixture-preview-version')
  await page.getByRole('tab', { name: '本地账号', exact: true }).click()
  await page.getByRole('button', { name: '关联已有账号', exact: true }).click()
  const linkDialog = page.getByRole('dialog', { name: '关联已有账号', exact: true })
  await linkDialog.getByLabel('本地账号 ID').fill('200')
  await linkDialog.getByLabel('上游 Key', { exact: true }).selectOption('3')
  await linkDialog.getByRole('button', { name: '确认', exact: true }).click()
  await page.getByText('账号关联已保存', { exact: true }).waitFor()
  assert.equal(links.length, 1)
  assert.equal('credentials' in links[0], false)
  await page.getByRole('tab', { name: '上游 Key', exact: false }).click()
  await page.getByRole('button', { name: '在上游创建 Key', exact: true }).click()
  const createDialog = page.getByRole('dialog', { name: '在上游创建 Key', exact: true })
  await createDialog.getByLabel('新密钥名称').fill('Browser fixture key')
  await createDialog.getByLabel('上游分组', { exact: true }).selectOption('vip')
  assert.match(await createDialog.locator('[data-upstream-group-detail]').textContent(), /当前分组的完整介绍/)
  assert.equal(await createDialog.getByRole('button', { name: '在上游创建 Key', exact: true }).isDisabled(), true)
  await createDialog.getByRole('checkbox', { name: '我确认在上游创建这把密钥', exact: true }).check()
  await createDialog.getByRole('button', { name: '在上游创建 Key', exact: true }).click()
  await page.getByText('已提交创建，请同步列表确认', { exact: true }).waitFor()
  assert.equal(creations.length, 1)
  assert.equal(creations[0].confirmed, true)
  await page.getByRole('row').filter({ has: page.getByText('主用 Key', { exact: true }) }).getByRole('button', { name: '切换分组', exact: true }).click()
  const groupDialog = page.getByRole('dialog', { name: '切换分组', exact: true })
  await groupDialog.getByLabel('上游分组', { exact: true }).selectOption('fast')
  assert.match(await groupDialog.locator('[data-upstream-group-detail]').textContent(), /×0.15/)
  assert.match(await groupDialog.locator('[data-upstream-group-detail]').textContent(), /第二行：完整介绍不截断/)
  assert.equal(await groupDialog.locator('script').count(), 0)
  assert.equal(await page.evaluate(() => window.upstreamInjected), undefined)
  for (const width of [1440, 375, 320]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.screenshot({ path: path.join(output, `key-group-${width}.png`), animations: 'disabled' })
    assert.equal(await groupDialog.locator('.modal-content').evaluate(element => element.scrollWidth <= element.clientWidth + 2), true, `Group dialog overflow at ${width}`)
  }
  await groupDialog.getByRole('button', { name: '保存到上游', exact: true }).click()
  await page.getByText('上游分组已切换并核验', { exact: true }).waitFor()
  assert.deepEqual(groupChanges, [{ revision: 1, current_group_id: 'vip', group_id: 'fast' }])
  await page.reload()
  await page.getByRole('main').getByText('快速分组', { exact: false }).first().waitFor()
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
  await writeFile(path.join(output, 'result.json'), JSON.stringify({ errors, networkFailures, imports, creations, policies, modelWrites, links, groupChanges, realRequests: 0 }, null, 2))
} catch (error) {
  for (const page of browser.contexts().flatMap(context => context.pages())) {
    await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {})
    await writeFile(path.join(output, 'failure.html'), await page.content()).catch(() => {})
  }
  await writeFile(path.join(output, 'errors.json'), JSON.stringify({ errors, networkFailures, error: String(error) }, null, 2))
  throw error
} finally { await browser.close() }
