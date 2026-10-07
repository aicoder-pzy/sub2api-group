<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AccountTestModal from '@/components/account/AccountTestModal.vue'
import MihomoPoolSettings from './settings/MihomoPoolSettings.vue'
import { bpsTicketsApi, type BPSTicketDashboard, type BPSTicketSettings, type BPSTicketAccount, type StateProbeResult } from '@/api/admin/bpsTickets'
import { getAll as getProxies } from '@/api/admin/proxies'
import { getById as getAccount } from '@/api/admin/accounts'
import type { Account, Proxy } from '@/types'

const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const dashboard = ref<BPSTicketDashboard>()
const proxies = ref<Proxy[]>([])
const error = ref('')
const notice = ref('')
const busy = ref('')
const search = ref('')
const models = ref<Record<number, string>>({})
const selected = ref<Record<number, string>>({})
const manualResults = ref<Record<number, StateProbeResult>>({})
const testAccount = ref<Account | null>(null)
const tab = ref<'accounts' | 'settings' | 'proxies'>('accounts')
const rows = computed(() => dashboard.value?.accounts.filter(a => `${a.id} ${a.name}`.toLowerCase().includes(search.value.toLowerCase())) || [])
type NumericKey = 'ttl_seconds' | 'refresh_before_seconds' | 'harvest_interval_seconds' | 'attempt_timeout_seconds' | 'max_attempts' | 'probe_interval_seconds' | 'degraded_threshold' | 'healthy_threshold'
const numericFields: { key: NumericKey; zh: string; en: string; min: number; max: number }[] = [
  { key: 'ttl_seconds', zh: '票据有效期（秒）', en: 'Ticket TTL (seconds)', min: 60, max: 240 },
  { key: 'refresh_before_seconds', zh: '提前刷新（秒）', en: 'Refresh before expiry (seconds)', min: 10, max: 239 },
  { key: 'harvest_interval_seconds', zh: '采票轮次间隔（秒）', en: 'Harvest interval (seconds)', min: 30, max: 3600 },
  { key: 'attempt_timeout_seconds', zh: '单次采票超时（秒）', en: 'Mint timeout (seconds)', min: 5, max: 90 },
  { key: 'max_attempts', zh: '每轮最多尝试次数', en: 'Maximum mint attempts per round', min: 1, max: 6 },
  { key: 'probe_interval_seconds', zh: '定时探针间隔（秒）', en: 'State probe interval (seconds)', min: 120, max: 86400 },
  { key: 'degraded_threshold', zh: '连续疑似异常次数 → BPS', en: 'Consecutive suspicious results → BPS', min: 1, max: 10 },
  { key: 'healthy_threshold', zh: '连续未见异常次数 → 恢复', en: 'Consecutive clear results → restore', min: 1, max: 10 }
]
function errorText(e: unknown) { return e && typeof e === 'object' && 'message' in e ? String(e.message) : String(e) }
async function act(key: string, fn: () => Promise<void>) {
  busy.value = key; error.value = ''; notice.value = ''
  try { await fn() } catch (e) { error.value = errorText(e) } finally { busy.value = '' }
}
async function load() {
  const [data, pool] = await Promise.all([bpsTicketsApi.get(), getProxies()])
  dashboard.value = data; proxies.value = pool
  for (const a of data.accounts) {
    models.value[a.id] = a.config.models.join('\n')
    selected.value[a.id] = a.config.models[0] || ''
  }
}
function modelList(a: BPSTicketAccount) { return (models.value[a.id] || '').split(/[\s,，]+/).filter(Boolean) }
async function saveAccount(a: BPSTicketAccount) {
  await act(`save-${a.id}`, async () => {
    a.config.models = [...new Set(modelList(a))]
    await bpsTicketsApi.saveAccount(a.id, a.config)
    notice.value = text('账号设置已保存', 'Account settings saved')
    selected.value[a.id] ||= a.config.models[0] || ''
  })
}
async function run(a: BPSTicketAccount, mode: 'probe' | 'harvest') {
  await act(`${mode}-${a.id}`, async () => {
    if (mode === 'probe') manualResults.value[a.id] = await bpsTicketsApi.probe(a.id, selected.value[a.id] || '')
    else {
      const result = await bpsTicketsApi.harvest(a.id, selected.value[a.id] || '')
      const index = a.tickets.findIndex(t => t.model === result.model)
      if (index >= 0) a.tickets[index] = result; else a.tickets.push(result)
    }
  })
}
function verdict(v?: string) {
  return v === 'healthy' ? text('未见异常', 'No anomaly observed') : v === 'degraded' ? text('疑似异常', 'Suspected anomaly') : text('无法判断', 'Inconclusive')
}
function time(value?: string) { return value && !value.startsWith('0001') ? new Date(value).toLocaleString() : '—' }
async function saveSettings(settings: BPSTicketSettings) {
  await act('settings', async () => { await bpsTicketsApi.saveSettings(settings); notice.value = text('全局设置已保存', 'Global settings saved') })
}
onMounted(() => act('load', load))
</script>

<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h1 class="text-xl font-semibold">{{ text('BPS 与票据探针', 'BPS and ticket probes') }}</h1><p class="mt-1 text-sm text-gray-500">{{ text('按账号和模型配置，适用于所有分组。', 'Configure accounts and models across all groups.') }}</p></div>
        <button class="btn btn-secondary" :disabled="!!busy" @click="act('load', load)">{{ text('刷新状态', 'Refresh status') }}</button>
      </div>
      <div v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-900/20">{{ error }}</div>
      <p v-if="notice" role="status" class="text-green-600">{{ notice }}</p>
      <p v-if="busy" role="status" class="text-sm text-primary-600">{{ text('处理中…采票和探针可能需要数十秒。', 'Working… ticket minting and probes can take several seconds.') }}</p>
      <nav class="flex gap-2" :aria-label="text('BPS 功能导航', 'BPS sections')">
        <button v-for="section in (['accounts', 'settings', 'proxies'] as const)" :key="section" class="btn" :class="tab === section ? 'btn-primary' : 'btn-secondary'" @click="tab = section">{{ section === 'accounts' ? text('账号与状态', 'Accounts') : section === 'settings' ? text('全局配置', 'Global settings') : text('Mihomo 代理池', 'Mihomo pool') }}</button>
      </nav>

      <MihomoPoolSettings v-if="tab === 'proxies'" />
      <template v-else-if="dashboard">
        <form v-if="tab === 'settings'" class="card space-y-5 p-5" @submit.prevent="saveSettings(dashboard.settings)">
          <div class="flex flex-wrap gap-5">
            <label class="flex items-center gap-2"><input v-model="dashboard.settings.harvest_enabled" type="checkbox" />{{ text('启用后台采票', 'Enable background harvesting') }}</label>
            <label class="flex items-center gap-2"><input v-model="dashboard.settings.auto_probe_enabled" type="checkbox" />{{ text('启用定时探针', 'Enable scheduled probes') }}</label>
            <label class="flex items-center gap-2"><input v-model="dashboard.settings.fail_closed" type="checkbox" />{{ text('缺少有效票据时拒绝该账号转发', 'Reject forwarding without a valid ticket') }}</label>
          </div>
          <p class="text-sm text-gray-500">{{ text('采票和探针会发送真实请求并消耗上游额度，仅处理已启用、可调度且勾选相应功能的账号。关闭拒绝模式时，无票请求仍按原生通道转发。', 'Harvesting and probes consume upstream quota. Background jobs only process active, schedulable, opted-in accounts. With rejection disabled, missing tickets allow ordinary native forwarding.') }}</p>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <label class="space-y-1"><span>{{ text('采票类型', 'Ticket format') }}</span><select v-model.number="dashboard.settings.target_length" class="input"><option :value="780">780 — {{ text('完整请求', 'Full Responses') }}</option><option :value="292">292 / 332 — {{ text('旧版 Lite', 'Legacy Lite') }}</option></select></label>
            <label class="space-y-1"><span>{{ text('目标网关', 'Target gateway') }}</span><input v-model="dashboard.settings.target_gateway" class="input" placeholder="any / unified-88" required /></label>
            <label class="space-y-1"><span>{{ text('采票出口', 'Harvest egress') }}</span><select v-model="dashboard.settings.harvest_proxy_source" class="input"><option value="account">{{ text('账号原代理', 'Account proxy') }}</option><option value="static">{{ text('静态代理池', 'Static proxy pool') }}</option><option value="mihomo">Mihomo</option></select></label>
            <label v-for="field in numericFields" :key="field.key" class="space-y-1"><span>{{ text(field.zh, field.en) }}</span><input v-model.number="dashboard.settings[field.key]" class="input" type="number" :min="field.min" :max="field.max" required /></label>
          </div>
          <fieldset class="space-y-2"><legend class="font-medium">{{ text('静态代理池（BPS 与采票共用成员）', 'Static pool members for BPS and harvesting') }}</legend><p class="text-xs text-gray-500">{{ text('在“代理管理”添加 HTTP / SOCKS 代理后选择。代理池为空或不可用时不会退回直连。', 'Add HTTP/SOCKS proxies in Proxy Management. A selected pool never falls back to a direct connection.') }}</p><div class="grid max-h-48 gap-2 overflow-y-auto sm:grid-cols-3"><label v-for="proxy in proxies" :key="proxy.id" class="flex gap-2"><input v-model="dashboard.settings.proxy_ids" type="checkbox" :value="proxy.id" />{{ proxy.name }} · #{{ proxy.id }}</label></div></fieldset>
          <button class="btn btn-primary" :disabled="!!busy">{{ text('保存全局配置', 'Save global settings') }}</button>
        </form>

        <div v-else class="space-y-4">
	      <p class="text-sm text-gray-500">{{ text('全局后台采票', 'Global harvesting') }}: {{ dashboard.settings.harvest_enabled ? text('开启', 'On') : text('关闭', 'Off') }} · {{ text('全局定时探针', 'Global scheduled probes') }}: {{ dashboard.settings.auto_probe_enabled ? text('开启', 'On') : text('关闭', 'Off') }}</p>
          <p class="rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ text(dashboard.limitation, 'Two completed requests examine a 780-ticket transition. This is a heuristic, not proof of model identity or intelligence. Other ticket formats, changed routes and incomplete responses are inconclusive. Manual probes do not change routing.') }}</p>
          <input v-model="search" class="input max-w-md" type="search" :placeholder="text('搜索账号名称或 ID', 'Search account name or ID')" :aria-label="text('搜索账号', 'Search accounts')" />
          <p v-if="!rows.length" class="p-8 text-center text-gray-500">{{ text('没有符合条件的 OpenAI OAuth 账号', 'No eligible OpenAI OAuth accounts') }}</p>
          <article v-for="a in rows" :key="a.id" class="card space-y-4 p-5">
            <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold">{{ a.name }} <span class="text-sm font-normal text-gray-500">#{{ a.id }} · {{ a.status }}</span></h2><span v-if="!a.schedulable" class="badge badge-gray">{{ text('暂停调度，后台任务跳过', 'Paused; background jobs skip this account') }}</span></div>
            <div class="grid gap-4 md:grid-cols-2">
              <label class="space-y-1"><span class="text-sm">{{ text('启用模型（每行一个，支持账号模型映射）', 'Models (one per line; account mappings apply)') }}</span><textarea v-model="models[a.id]" class="input" rows="2" placeholder="gpt-6-astra" /></label>
              <div class="space-y-2 text-sm">
                <label class="flex gap-2"><input v-model="a.config.bps" type="checkbox" :disabled="!a.bps_eligible" />{{ text('手动启用 BPS', 'Enable BPS manually') }}</label>
                <label class="flex gap-2"><input v-model="a.config.tickets" type="checkbox" />{{ text('后台采票 / 使用票据', 'Harvest and use tickets') }}</label>
                <label class="flex gap-2"><input v-model="a.config.auto_probe" type="checkbox" @change="!a.config.auto_probe && (a.config.auto_switch = false)" />{{ text('定时检测票据状态', 'Schedule ticket state probes') }}</label>
                <label class="flex gap-2"><input v-model="a.config.auto_switch" type="checkbox" :disabled="!a.bps_eligible || !a.config.auto_probe" />{{ text('疑似异常时自动切 BPS，恢复后撤销自动切换', 'Automatically enable BPS on anomalies and restore after recovery') }}</label>
                <label class="flex items-center gap-2"><span>{{ text('BPS 出口', 'BPS egress') }}</span><select v-model="a.config.proxy_source" class="input w-auto"><option value="account">{{ text('账号原代理', 'Account proxy') }}</option><option value="static">{{ text('静态代理池', 'Static pool') }}</option><option value="mihomo">Mihomo</option></select></label>
                <p v-if="!a.bps_eligible" class="text-amber-600">{{ text('Free 套餐不支持 BPS', 'Free plans do not support BPS') }}</p>
              </div>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button class="btn btn-primary btn-sm" :disabled="!!busy" @click="saveAccount(a)">{{ text('保存账号设置', 'Save account settings') }}</button>
              <select v-model="selected[a.id]" class="input w-auto" :aria-label="text('探针模型', 'Probe model')"><option v-for="model in modelList(a)" :key="model" :value="model">{{ model }}</option></select>
              <button class="btn btn-secondary btn-sm" :disabled="!!busy || !selected[a.id]" @click="run(a, 'probe')">{{ text('手动票据探针', 'Run state probe') }}</button>
              <button class="btn btn-secondary btn-sm" :disabled="!!busy || !selected[a.id]" @click="run(a, 'harvest')">{{ text('立即采票', 'Harvest now') }}</button>
              <button class="btn btn-secondary btn-sm" :disabled="!!busy" @click="act(`test-${a.id}`, async () => { testAccount = await getAccount(a.id) })">{{ text('测试转发', 'Test forwarding') }}</button>
            </div>
            <p class="text-xs text-gray-500">{{ text('修改后先保存。手动探针只检测原生通道，不触发自动切换。BPS 不使用票据；手动开关优先于自动恢复。', 'Save changes before testing. Manual probes inspect the native route without changing it. BPS bypasses tickets; manual BPS stays enabled after automatic recovery.') }}</p>
            <div v-if="manualResults[a.id]" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800" role="status"><strong>{{ verdict(manualResults[a.id]?.verdict) }}</strong> · {{ manualResults[a.id]?.reason }}<p class="mt-1 text-xs">{{ time(manualResults[a.id]?.finished_at) }} · {{ manualResults[a.id]?.ticket_length }} → {{ manualResults[a.id]?.continue_ticket_length }} · {{ manualResults[a.id]?.latency_ms }} ms</p></div>
            <div v-for="(state, model) in a.state.models" :key="model" class="text-sm"><strong>{{ model }}</strong> · {{ verdict(state.probe?.verdict) }} · {{ state.auto_bps ? text('自动 BPS 已生效', 'Automatic BPS active') : text('自动 BPS 未生效', 'Automatic BPS inactive') }} · {{ time(state.probe?.finished_at) }}</div>
            <div v-for="ticket in a.tickets" :key="ticket.model" class="flex flex-wrap gap-x-3 text-xs text-gray-500"><span>{{ ticket.model }} · {{ ticket.ready && new Date(ticket.expires_at).getTime() > Date.now() ? text('票据就绪', 'Ticket ready') : text('无有效票据', 'No valid ticket') }}</span><span>{{ ticket.length || '—' }} · {{ ticket.reason }}</span><span>{{ text('到期', 'Expires') }} {{ time(ticket.expires_at) }}</span><span v-if="ticket.node">{{ ticket.node }}</span></div>
          </article>
        </div>
      </template>
      <AccountTestModal :show="!!testAccount" :account="testAccount" @close="testAccount = null" />
    </div>
  </AppLayout>
</template>
