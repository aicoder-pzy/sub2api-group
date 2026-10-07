<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'

interface WarmPool { target: number; ready: number; checking: number; cooling: number }
interface MihomoStatus {
  installed: boolean; running: boolean; supported: boolean; busy: boolean; phase: string; error?: string; endpoint: string; use_once: boolean
  subscription_download_mode: 'auto' | 'proxy' | 'direct'
  subscription_items: { id: string; label: string; enabled: boolean; nodes: number }[]
  node_states: { name: string; display_name?: string; state: string; country_code?: string; dynamic: boolean }[]
  country_filter: { mode: string; codes: string[]; allow_unknown: boolean; dynamic_provider_managed: boolean }
  bps_warm_pool: WarmPool; bps_ip_warm_pool: WarmPool
}
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const status = ref<MihomoStatus>()
const busy = ref(false)
const error = ref('')
const subscription = ref('')
const subscriptionName = ref('')
const dynamic = ref('')
const search = ref('')
const countryCodes = ref('')
const countryMode = ref('off')
const allowUnknown = ref(false)
const providerManaged = ref(true)
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
async function refresh() {
  try {
    const { data } = await apiClient.get<MihomoStatus>('/admin/system/mihomo')
    if (disposed) return
    status.value = data
    if (data.busy) timer = setTimeout(refresh, 2000)
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
}
async function action(action: string, extra: Record<string, unknown> = {}) {
  busy.value = true; error.value = ''
  try {
    await apiClient.post('/admin/system/mihomo', { action, ...extra })
    if (action === 'subscription_add') { subscription.value = ''; subscriptionName.value = '' }
    if (action === 'dynamic_append') dynamic.value = ''
    await refresh()
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) } finally { busy.value = false }
}
async function removeSource(id: string) {
  if (window.confirm(text('移除此订阅及其独有节点？', 'Remove this subscription and its exclusive nodes?'))) await action(`subscription_remove/${id}`)
}
async function downloadMode(event: Event) {
  busy.value = true
  try { await apiClient.put('/admin/system/mihomo/download-mode', { mode: (event.target as HTMLSelectElement).value }); await refresh() }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) } finally { busy.value = false }
}
onMounted(async () => {
  await refresh()
  if (status.value) { countryMode.value = status.value.country_filter.mode || 'off'; countryCodes.value = status.value.country_filter.codes?.join(', ') || ''; allowUnknown.value = status.value.country_filter.allow_unknown; providerManaged.value = status.value.country_filter.dynamic_provider_managed }
})
onUnmounted(() => { disposed = true; clearTimeout(timer) })
</script>

<template>
  <section class="card space-y-5 p-5" :aria-busy="busy || status?.busy">
    <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold">Mihomo · {{ status?.phase || '—' }}</h2><button class="btn btn-secondary btn-sm" :disabled="busy" @click="refresh">{{ text('刷新代理池', 'Refresh pool') }}</button></div>
    <p v-if="error || status?.error" role="alert" class="text-red-600">{{ error || status?.error }}</p>
    <p class="text-sm text-gray-500">{{ text('Mihomo 内核管理、订阅与动态代理。BPS 使用预热出口，采票可轮换节点；票据转发固定回采票出口。', 'Manage the Mihomo kernel, subscriptions and dynamic proxies. BPS uses warm exits; harvesting rotates nodes and ticket requests return to the minting exit.') }}</p>
    <template v-if="status">
      <p v-if="!status.supported" class="text-amber-600">{{ text('Mihomo 内核管理需要 Linux amd64 / arm64；此界面在服务器部署后可用。', 'Kernel management requires Linux amd64/arm64 and becomes available after server deployment.') }}</p>
      <div class="flex flex-wrap items-center gap-3"><span class="text-sm">{{ status.endpoint }} · {{ status.node_states?.length || 0 }} {{ text('个节点', 'nodes') }}</span><button class="btn btn-secondary btn-sm" :disabled="busy || status.busy || !status.supported" @click="action(status.installed ? 'start' : 'install')">{{ status.installed ? text('启动 / 重载内核', 'Start / reload kernel') : text('安装内核', 'Install kernel') }}</button><label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="status.use_once" :disabled="busy || status.busy" @change="action(status.use_once ? 'once_off' : 'once_on')" />{{ text('采票节点使用一次后退出采集池', 'Retire a node after one harvest attempt') }}</label></div>
      <div class="grid gap-3 sm:grid-cols-2"><div v-for="pool in [{ name: 'Mihomo', value: status.bps_warm_pool }, { name: text('静态代理池', 'Static pool'), value: status.bps_ip_warm_pool }]" :key="pool.name" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800"><strong>{{ pool.name }}</strong><p>{{ text('就绪 / 目标', 'Ready / target') }} {{ pool.value?.ready || 0 }} / {{ pool.value?.target || 0 }} · {{ text('预热中', 'Checking') }} {{ pool.value?.checking || 0 }} · {{ text('冷却', 'Cooling') }} {{ pool.value?.cooling || 0 }}</p></div></div>

      <form class="space-y-3 border-t pt-4 dark:border-dark-700" @submit.prevent="action('subscription_add', { subscriptions: [subscription.trim()], name: subscriptionName })">
        <h3 class="font-medium">{{ text('添加订阅', 'Add subscription') }}</h3>
        <div class="flex flex-wrap gap-2"><input v-model="subscriptionName" class="input sm:max-w-xs" :placeholder="text('订阅名称', 'Subscription name')" :aria-label="text('订阅名称', 'Subscription name')" maxlength="80" /><input v-model="subscription" class="input min-w-60 flex-1" type="url" placeholder="https://…" :aria-label="text('订阅地址', 'Subscription URL')" autocomplete="off" required /><button class="btn btn-primary" :disabled="busy || status.busy || !status.installed">{{ text('添加', 'Add') }}</button></div>
        <label class="flex flex-wrap items-center gap-2 text-sm">{{ text('订阅下载方式', 'Subscription download mode') }}<select class="input w-auto" :value="status.subscription_download_mode" :disabled="busy || status.busy" @change="downloadMode"><option value="auto">{{ text('自动：代理失败后直连', 'Auto: proxy, then direct') }}</option><option value="proxy">{{ text('仅代理', 'Proxy only') }}</option><option value="direct">{{ text('仅直连', 'Direct only') }}</option></select></label>
        <div v-for="source in status.subscription_items" :key="source.id" class="flex flex-wrap items-center gap-2 text-sm"><span class="mr-auto">{{ source.label || source.id.slice(0, 12) }} · {{ source.nodes }} {{ text('节点', 'nodes') }}</span><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || status.busy" @click="action(`subscription_refresh/${source.id}`)">{{ text('更新', 'Refresh') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || status.busy" @click="action(`${source.enabled ? 'subscription_disable' : 'subscription_enable'}/${source.id}`)">{{ source.enabled ? text('停用', 'Disable') : text('启用', 'Enable') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || status.busy" @click="removeSource(source.id)">{{ text('移除', 'Remove') }}</button></div>
      </form>

      <form class="space-y-3 border-t pt-4 dark:border-dark-700" @submit.prevent="action('dynamic_append', { dynamic_proxies: dynamic.split(/\r?\n/).map(v => v.trim()).filter(Boolean) })"><label class="block space-y-2"><span class="font-medium">{{ text('添加动态代理（每行一个）', 'Add dynamic proxies (one per line)') }}</span><textarea v-model="dynamic" class="input" rows="2" placeholder="http://username:password@host:port" autocomplete="off" required /></label><button class="btn btn-primary" :disabled="busy || status.busy || !status.installed">{{ text('添加动态代理', 'Add dynamic proxies') }}</button></form>

      <form class="space-y-3 border-t pt-4 dark:border-dark-700" @submit.prevent="action('country_filter', { country_filter: { mode: countryMode, codes: countryCodes.toUpperCase().split(/[\s,，]+/).filter(Boolean), allow_unknown: allowUnknown, dynamic_provider_managed: providerManaged } })"><h3 class="font-medium">{{ text('出口地区筛选', 'Exit country filter') }}</h3><div class="flex flex-wrap gap-3"><select v-model="countryMode" class="input w-auto" :aria-label="text('地区筛选方式', 'Country filter mode')"><option value="off">{{ text('关闭', 'Off') }}</option><option value="include">{{ text('仅包含', 'Include only') }}</option><option value="exclude">{{ text('排除', 'Exclude') }}</option></select><input v-model="countryCodes" class="input w-auto" placeholder="US, JP, SG" :aria-label="text('国家代码', 'Country codes')" /><label class="flex items-center gap-2 text-sm"><input v-model="allowUnknown" type="checkbox" />{{ text('允许未知地区', 'Allow unknown countries') }}</label><label class="flex items-center gap-2 text-sm"><input v-model="providerManaged" type="checkbox" />{{ text('动态代理地区由供应商管理', 'Dynamic country managed by provider') }}</label><button class="btn btn-secondary btn-sm" :disabled="busy || status.busy">{{ text('保存地区筛选', 'Save filter') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || status.busy" @click="action('country_scan')">{{ text('检测节点地区', 'Detect countries') }}</button></div></form>

      <div class="space-y-3 border-t pt-4 dark:border-dark-700"><input v-model="search" class="input sm:max-w-sm" type="search" :placeholder="text('搜索节点', 'Search nodes')" :aria-label="text('搜索节点', 'Search nodes')" /><div class="max-h-96 space-y-2 overflow-y-auto"><div v-for="node in status.node_states?.filter(n => `${n.display_name} ${n.name}`.toLowerCase().includes(search.toLowerCase()))" :key="node.name" class="flex flex-wrap items-center gap-3 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800"><span class="mr-auto">{{ node.display_name || node.name }} · {{ node.country_code || '—' }} · {{ node.state }} {{ node.dynamic ? '(dynamic)' : '' }}</span><button class="btn btn-secondary btn-sm" :disabled="busy || status.busy" @click="action(`probe/${node.name}`)">{{ text('探测出口', 'Probe exit') }}</button><button class="btn btn-secondary btn-sm" :disabled="busy || status.busy" @click="action(`${node.state === 'enabled' ? 'disable' : 'recover'}/${node.name}`)">{{ node.state === 'enabled' ? text('停用', 'Disable') : text('恢复', 'Recover') }}</button></div></div></div>
    </template>
  </section>
</template>
