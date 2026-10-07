<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { prismApi, type PrismDashboard, type PrismTestResult } from '@/api/admin/prism'

const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const dashboard = ref<PrismDashboard>()
const savedSettings = ref('')
const bridgeKey = ref('')
const busy = ref('')
const error = ref('')
const notice = ref('')
const ready = ref<boolean | null>(null)
const accountID = ref<number | null>(null)
const model = ref('gpt-5.6-sol')
const effort = ref('medium')
const prompt = ref('Reply with OK.')
const result = ref<PrismTestResult>()
const testedAccount = ref('')
const accounts = computed(() => dashboard.value?.accounts.filter(a => a.status === 'active') || [])
const settingsSaved = computed(() => JSON.stringify(dashboard.value?.settings) === savedSettings.value && !bridgeKey.value)
const canTest = computed(() => settingsSaved.value && dashboard.value?.settings.enabled && dashboard.value.settings.api_key_configured && accountID.value && prompt.value.trim() && !busy.value)

async function act(name: string, fn: () => Promise<void>) {
  busy.value = name; error.value = ''; notice.value = ''
  try { await fn() } catch (e) {
    error.value = e && typeof e === 'object' && 'message' in e ? String(e.message) : String(e)
  } finally { busy.value = '' }
}
async function load() {
  dashboard.value = await prismApi.get()
  savedSettings.value = JSON.stringify(dashboard.value.settings)
  if (!accounts.value.some(a => a.id === accountID.value)) accountID.value = accounts.value[0]?.id ?? null
}
async function save() {
  if (!dashboard.value) return
  await act('save', async () => {
    const current = dashboard.value!.settings
    dashboard.value!.settings = await prismApi.save({ enabled: current.enabled, base_url: current.base_url, api_key: bridgeKey.value })
    savedSettings.value = JSON.stringify(dashboard.value!.settings)
    bridgeKey.value = ''; ready.value = null
    notice.value = text('Prism 测试设置已保存', 'Prism test settings saved')
  })
}
async function test() {
  if (!canTest.value || !accountID.value) return
  const id = accountID.value
  result.value = undefined
  testedAccount.value = accounts.value.find(a => a.id === id)?.name || String(id)
  await act('test', async () => {
    result.value = await prismApi.test(id, { model: model.value, effort: effort.value, prompt: prompt.value })
  })
}
onMounted(() => act('load', load))
</script>

<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-5">
      <div class="flex items-center justify-between gap-3">
        <h1 class="text-xl font-semibold">{{ text('Prism 管理员测试', 'Prism administrator tests') }}</h1>
        <button class="btn btn-secondary" :disabled="!!busy" @click="act('load', load)">{{ text('刷新', 'Refresh') }}</button>
      </div>
      <p class="text-sm text-gray-500">{{ text('测试现有 OAuth 账号在 Prism 上的可用模型。测试使用上游额度，不向下游开放，不按 token 计费，也不修改账号路由。', 'Test Prism models with an existing OAuth account. Tests consume upstream quota, are not available to downstream clients, do not bill tokens, and do not change account routing.') }}</p>
      <div v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-900/20">{{ error }}</div>
      <p v-if="notice" role="status" class="text-green-600">{{ notice }}</p>
      <p v-if="busy" role="status" class="text-sm text-primary-600">{{ busy === 'test' ? text('测试中，最长约 5 分钟，请勿重复提交。', 'Testing, up to 5 minutes. Do not submit again.') : text('处理中…', 'Working…') }}</p>
      <template v-if="dashboard">
        <form class="card space-y-4 p-5" @submit.prevent="save">
          <h2 class="font-semibold">{{ text('适配器设置', 'Adapter settings') }}</h2>
          <label class="flex items-center gap-2">
            <input v-model="dashboard.settings.enabled" data-testid="enabled" type="checkbox" :disabled="!!busy" />
            <span>{{ text('启用管理员测试', 'Enable administrator tests') }}</span>
          </label>
          <label class="block space-y-1">
            <span>{{ text('服务器适配器地址', 'Server adapter URL') }}</span>
            <input v-model="dashboard.settings.base_url" data-testid="endpoint" class="input" required :disabled="!!busy" />
          </label>
          <label class="block space-y-1">
            <span>{{ text('桥接密钥', 'Bridge key') }}</span>
            <input v-model="bridgeKey" data-testid="bridge-key" class="input" type="password" autocomplete="new-password" maxlength="256" :disabled="!!busy" :placeholder="dashboard.settings.api_key_configured ? text('已配置，留空保留', 'Configured; leave blank to keep') : text('与服务器适配器使用相同的密钥，至少 32 字符', 'Same key as the server adapter, at least 32 characters')" />
          </label>
          <div class="flex flex-wrap items-center gap-3">
            <button class="btn btn-primary" type="submit" :disabled="!!busy">{{ text('保存设置', 'Save settings') }}</button>
            <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="act('health', async () => { ready = (await prismApi.health()).process_ready })">{{ text('检查已保存地址的服务状态', 'Check saved adapter endpoint') }}</button>
            <span v-if="ready !== null" role="status">{{ ready ? text('服务进程可访问', 'Service process reachable') : text('服务未就绪', 'Service not ready') }}</span>
          </div>
          <p class="text-xs text-gray-500">{{ text('服务状态正常仅代表适配器进程可访问；账号登录和模型权限需要通过下面的测试确认。', 'A reachable process does not verify account login or model access. Use the test below to check those.') }}</p>
        </form>
        <form class="card space-y-4 p-5" @submit.prevent="test">
          <h2 class="font-semibold">{{ text('账号文本测试', 'Account text test') }}</h2>
          <p v-if="!settingsSaved" class="text-sm text-amber-600">{{ text('请先保存适配器设置，再运行测试。', 'Save adapter settings before running a test.') }}</p>
          <p class="text-sm text-gray-500">{{ text('模型列表表示适配器支持范围，不代表账号已获得权限。不支持 Astra；不可用时直接报错，不替换模型。', 'These models are supported by the adapter but may be unavailable to this account. Astra is not supported. Unavailable models return an error without substitution.') }}</p>
          <label class="block space-y-1">
            <span>{{ text('OAuth 账号', 'OAuth account') }}</span>
            <select v-model="accountID" data-testid="account" class="input" :disabled="!!busy || !accounts.length">
              <option v-for="a in accounts" :key="a.id" :value="a.id">#{{ a.id }} · {{ a.name }}</option>
            </select>
          </label>
          <p v-if="!accounts.length" class="text-sm text-gray-500">{{ text('没有可测试的启用中 OAuth 账号。', 'No active OAuth accounts are available for testing.') }}</p>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block space-y-1"><span>{{ text('模型', 'Model') }}</span><select v-model="model" data-testid="model" class="input" :disabled="!!busy"><option v-for="m in dashboard.models" :key="m" :value="m">{{ m }}</option></select></label>
            <label class="block space-y-1"><span>{{ text('思考强度', 'Reasoning effort') }}</span><select v-model="effort" data-testid="effort" class="input" :disabled="!!busy"><option v-for="e in dashboard.efforts" :key="e" :value="e">{{ e }}</option></select></label>
          </div>
          <label class="block space-y-1"><span>{{ text('测试内容（最多 4096 字节）', 'Test prompt (up to 4096 bytes)') }}</span><textarea v-model="prompt" data-testid="prompt" class="input" rows="3" maxlength="4096" required :disabled="!!busy" /></label>
          <button data-testid="run-test" class="btn btn-primary" type="submit" :disabled="!canTest">{{ text('运行一次测试', 'Run one test') }}</button>
        </form>
        <section v-if="result" class="card space-y-3 p-5" aria-live="polite">
          <h2 class="font-semibold">{{ text('测试完成', 'Test completed') }} · {{ testedAccount }}</h2>
          <p class="text-sm">{{ result.model }} · {{ result.effort }} · {{ (result.duration_ms / 1000).toFixed(1) }} s</p>
          <p class="break-all text-xs text-gray-500">{{ result.request_id }}</p>
          <pre class="whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-900">{{ result.text }}</pre>
          <p class="text-xs text-gray-500">{{ text('上游未提供可靠的 token 用量；本次不计费。通过只证明本次请求完成，不代表长期稳定性或模型能力保证。', 'Reliable upstream token usage is unavailable; this test is unbilled. Success confirms this request completed, not long-term reliability or model capability.') }}</p>
        </section>
      </template>
    </div>
  </AppLayout>
</template>
