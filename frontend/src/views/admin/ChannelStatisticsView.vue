<template>
  <AppLayout>
    <div class="space-y-5">
      <form class="card space-y-4 p-5" @submit.prevent="load">
        <div class="flex flex-wrap items-center gap-2">
          <button v-for="quick in quickRanges" :key="quick.key" type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="setRange(quick.days, quick.offset)">{{ text(quick.key) }}</button>
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ text('timezone') }}</span>
        </div>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
          <label class="space-y-1 text-sm">{{ text('start') }}<input v-model="filters.start_date" type="date" class="input w-full" :min="earliest" :max="today" required /></label>
          <label class="space-y-1 text-sm">{{ text('end') }}<input v-model="filters.end_date" type="date" class="input w-full" :min="filters.start_date || earliest" :max="today" required /></label>
          <label class="space-y-1 text-sm">{{ text('group') }}<select v-model="filters.group_id" class="input w-full" @change="load"><option :value="0">{{ text('allGroups') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option></select></label>
          <label class="space-y-1 text-sm">{{ text('model') }}<select v-model="filters.model" class="input w-full" @change="load"><option value="">{{ text('allModels') }}</option><option v-for="model in models" :key="model" :value="model">{{ model }}</option></select></label>
          <label class="space-y-1 text-sm">{{ text('effort') }}<select v-model="filters.effort" class="input w-full" @change="load"><option value="">{{ text('allEfforts') }}</option><option v-for="effort in efforts" :key="effort" :value="effort">{{ effort }}</option></select></label>
        </div>
        <div class="flex flex-wrap gap-2">
          <button type="submit" class="btn btn-primary" :disabled="loading">{{ loading ? t('common.loading') : text('refresh') }}</button>
          <button type="button" class="btn btn-secondary" :disabled="!data || loading" @click="exportCSV">{{ text('export') }}</button>
        </div>
        <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      </form>

      <div v-if="data" :aria-busy="loading" :class="{ 'opacity-60': loading }" class="space-y-5">
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm text-gray-500 dark:text-gray-400">
          <span>{{ data.start_date }} — {{ data.end_date }} · {{ appliedGroup }} · {{ applied.model || text('allModels') }} · {{ applied.effort || text('allEfforts') }}</span>
          <span>{{ text('updated', { time: updatedTime }) }}</span>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div class="card p-5"><p class="metric-label">{{ text('requests') }}</p><p class="metric-value">{{ count(data.summary.requests) }}</p><p class="mt-2 text-sm">{{ text('availability') }} <strong>{{ successRate(data.summary) }}</strong></p><p class="metric-detail">{{ count(data.summary.successes) }} / {{ count(data.summary.failures) }} {{ text('successFailure') }}</p></div>
          <div class="card p-5"><p class="metric-label">{{ text('speed') }} P50</p><p class="metric-value">{{ seconds(data.summary.ttft_p50_ms) }}</p><p class="mt-2 text-sm">P95 {{ seconds(data.summary.ttft_p95_ms) }}</p><p class="metric-detail">{{ text('latencySamples', { count: count(data.summary.latency_samples) }) }}</p></div>
          <div class="card p-5"><p class="metric-label">{{ text('cost') }} (USD)</p><p class="metric-value">{{ money(data.summary.account_cost) }}</p><p class="mt-2 text-sm">{{ text('userCost') }} {{ money(data.summary.user_cost) }}</p><p class="metric-detail">{{ text('standardCost') }} {{ money(data.summary.standard_cost) }}</p></div>
          <div class="card p-5"><p class="metric-label">{{ text('intelligence') }}</p><p class="metric-value">{{ normalRate(data.summary) }}</p><p class="mt-2 text-sm">{{ text('validTests', { count: data.summary.normal + data.summary.degraded }) }}</p><p class="metric-detail">{{ text('normal') }} {{ data.summary.normal }} · {{ text('degraded') }} {{ data.summary.degraded }}</p></div>
        </div>

        <div v-if="data.daily.length > 1" class="card p-5">
          <p class="mb-4 text-sm font-medium">{{ text('daily') }} · {{ text('cost') }} (USD)</p>
          <div class="flex items-end gap-3" style="height: 150px">
            <div v-for="day in data.daily" :key="day.day" class="flex min-w-0 flex-1 flex-col items-center justify-end gap-1" style="height: 100%" :title="`${day.day}: ${money(day.account_cost)} · ${count(day.requests)}`">
              <span class="text-xs tabular-nums">{{ money(day.account_cost) }}</span>
              <div class="w-full max-w-24 rounded-t bg-primary-500/80" :style="{ height: `${Math.max(2, day.account_cost / maxDailyCost * 100)}px` }" />
              <span class="text-xs text-gray-500">{{ day.day.slice(5) }}</span>
            </div>
          </div>
        </div>

        <div class="card overflow-hidden">
          <div class="flex flex-wrap items-center gap-2 border-b border-gray-200 p-4 dark:border-dark-700">
            <button v-for="tab in (['accounts', 'daily'] as const)" :key="tab" class="btn btn-sm" :class="view === tab ? 'btn-primary' : 'btn-secondary'" @click="view = tab">{{ text(tab) }}</button>
            <label v-if="view === 'accounts'" class="ml-auto flex items-center gap-2 text-sm">{{ text('sort') }}<select v-model="sort" class="input py-1"><option value="requests">{{ text('requestsSort') }}</option><option value="account_cost">{{ text('costSort') }}</option><option value="ttft_p95_ms">{{ text('speedSort') }}</option></select></label>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full whitespace-nowrap text-left text-sm tabular-nums">
              <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr>
                <th>{{ text(view === 'accounts' ? 'account' : 'date') }}</th><th>{{ text('requests') }}</th><th>{{ text('availability') }}</th><th>{{ text('ttft') }}</th><th>{{ text('slow') }}</th><th>{{ text('duration') }}</th><th>{{ text('throughput') }}</th><th>{{ text('intelligence') }}</th><th>{{ text('tokens') }}</th><th>{{ text('cost') }}</th><th>{{ text('userCost') }}</th>
              </tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="row in rows" :key="view === 'accounts' ? row.account_id : row.day" class="hover:bg-gray-50 dark:hover:bg-dark-800/50">
                  <td><div class="max-w-64 truncate font-medium" :title="row.account_name">{{ view === 'accounts' ? row.account_name : row.day }}</div><div v-if="view === 'accounts'" class="metric-detail">#{{ row.account_id }} <span v-if="row.account_deleted">· {{ text('deleted') }}</span><span v-if="row.successes + row.failures < 100"> · {{ text('limited') }}</span></div></td>
                  <td><strong>{{ count(row.requests) }}</strong><div class="metric-detail">{{ row.stream_requests }} / {{ row.nonstream_requests }}</div><div class="metric-detail">{{ text('samples') }}</div></td>
                  <td><strong :class="row.failures ? 'text-amber-600 dark:text-amber-400' : ''">{{ successRate(row) }}</strong><div class="metric-detail">{{ row.successes }} / {{ row.failures }}</div><div v-if="applied.effort && row.unclassified_failures" class="metric-detail" :title="text('unclassified', { count: row.unclassified_failures })">+{{ row.unclassified_failures }} {{ text('unknown') }}</div></td>
                  <td><strong>{{ seconds(row.ttft_p50_ms) }} / {{ seconds(row.ttft_p95_ms) }}</strong><div class="metric-detail">{{ text('latencySamples', { count: row.latency_samples }) }}</div><div class="metric-detail">{{ text('average', { value: seconds(row.ttft_average_ms) }) }}</div></td>
                  <td :class="row.slow_60 ? 'text-amber-600 dark:text-amber-400' : ''">{{ row.slow_30 }} / {{ row.slow_60 }}</td>
                  <td>{{ seconds(row.duration_p50_ms) }} / {{ seconds(row.duration_p95_ms) }}</td>
                  <td>{{ decimal(row.output_tps_p50) }} tok/s<div class="metric-detail">{{ text('context') }} {{ decimal(row.context_tokens_p50, 0) }}</div></td>
                  <td><strong :title="row.normal + row.degraded <= 10 ? text('fewTests') : ''" :class="row.degraded ? 'text-amber-600 dark:text-amber-400' : ''">{{ normalRate(row) }}</strong><div class="metric-detail">{{ text('normal') }} {{ row.normal }} / {{ text('degraded') }} {{ row.degraded }}</div><div class="metric-detail">{{ text('unknown') }} {{ row.unknown }} / {{ text('failed') }} {{ row.failed_tests }}</div></td>
                  <td><div>{{ text('input') }} {{ count(row.input_tokens) }} / {{ text('output') }} {{ count(row.output_tokens) }}</div><div class="metric-detail">{{ text('cacheRead') }} {{ count(row.cache_read_tokens) }} / {{ text('cacheWrite') }} {{ count(row.cache_creation_tokens) }}</div></td>
                  <td><strong>{{ money(row.account_cost) }}</strong><div class="metric-detail">USD</div></td><td>{{ money(row.user_cost) }}<div class="metric-detail">{{ text('standardCost') }} {{ money(row.standard_cost) }}</div></td>
                </tr>
                <tr v-if="rows.length === 0"><td colspan="11" class="py-12 text-center text-gray-500">{{ text('empty') }}</td></tr>
              </tbody>
            </table>
          </div>
        </div>
        <p v-if="applied.effort && data.summary.unclassified_failures" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ text('unclassified', { count: data.summary.unclassified_failures }) }}</p>
        <details class="card p-5 text-sm text-gray-500 dark:text-gray-400" open>
          <summary class="cursor-pointer font-medium text-gray-700 dark:text-gray-300">{{ text('methodology') }}</summary>
          <div class="mt-3 space-y-2"><p v-for="note in ['noteCompare', 'noteSuccess', 'noteSpeed', 'noteQuality', 'noteCost']" :key="note">{{ text(note) }}</p></div>
        </details>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { getAllIncludingInactive } from '@/api/admin/groups'
import { getChannelStatistics, type ChannelStatistics, type ChannelStatisticsQuery, type ChannelStatisticsRow } from '@/api/admin/channelStatistics'
import type { AdminGroup } from '@/types'

const { t, locale } = useI18n()
const text = (key: string, params: Record<string, string | number> = {}) => t(`channelStatistics.${key}`, params)
const beijingDate = (offset = 0) => new Date(Date.now() + 8 * 3600_000 + offset * 86400_000).toISOString().slice(0, 10)
const today = ref(beijingDate())
const earliest = computed(() => new Date(new Date(`${today.value}T00:00:00Z`).getTime() - 6 * 86400_000).toISOString().slice(0, 10))
// Date limits refresh with every query, including a page left open across midnight.
const minDate = () => beijingDate(-6)
const filters = reactive<ChannelStatisticsQuery>({ start_date: today.value, end_date: today.value, group_id: 0, model: 'gpt-6.1-sol', effort: '' })
const applied = ref<ChannelStatisticsQuery>({ ...filters })
const groups = ref<AdminGroup[]>([])
const data = ref<ChannelStatistics | null>(null)
const error = ref('')
const loading = ref(false)
const view = ref<'accounts' | 'daily'>('accounts')
const sort = ref<'requests' | 'account_cost' | 'ttft_p95_ms'>('requests')
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
const quickRanges = [{ key: 'today', days: 1, offset: 0 }, { key: 'yesterday', days: 1, offset: -1 }, { key: 'threeDays', days: 3, offset: 0 }, { key: 'sevenDays', days: 7, offset: 0 }]
let controller: AbortController | undefined
const models = computed(() => [...new Set([...(data.value?.models ?? []), ...(filters.model ? [filters.model] : [])])].sort())
const appliedGroup = computed(() => groups.value.find(g => g.id === applied.value.group_id)?.name ?? (applied.value.group_id ? `#${applied.value.group_id}` : text('allGroups')))
const updatedTime = computed(() => data.value ? new Intl.DateTimeFormat(locale.value, { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(data.value.generated_at)) : '')
const maxDailyCost = computed(() => Math.max(0.000001, ...(data.value?.daily.map(d => d.account_cost) ?? [])))
const rows = computed(() => {
  if (view.value === 'daily') return [...(data.value?.daily ?? [])].reverse()
  const key = sort.value
  return [...(data.value?.accounts ?? [])].sort((a, b) => {
    if (key === 'ttft_p95_ms') return (a[key] ?? Infinity) - (b[key] ?? Infinity) || a.account_id - b.account_id
    return b[key] - a[key] || a.account_id - b.account_id
  })
})
const count = (value: number) => value.toLocaleString(locale.value)
const money = (value: number) => `$${value.toLocaleString(locale.value, { minimumFractionDigits: 4, maximumFractionDigits: 4 })}`
const decimal = (value: number | null, digits = 1) => value == null ? '—' : value.toLocaleString(locale.value, { maximumFractionDigits: digits })
const seconds = (value: number | null) => value == null ? '—' : `${(value / 1000).toFixed(2)}s`
const successRate = (row: ChannelStatisticsRow) => row.successes + row.failures ? `${(row.successes / (row.successes + row.failures) * 100).toFixed(2)}%` : '—'
const normalRate = (row: ChannelStatisticsRow) => row.normal + row.degraded > 10 ? `${(row.normal / (row.normal + row.degraded) * 100).toFixed(1)}%` : '—'

async function load() {
  today.value = beijingDate()
  if (!filters.start_date || !filters.end_date || filters.start_date < minDate() || filters.end_date > today.value || filters.end_date < filters.start_date) {
    error.value = text('rangeError')
    return
  }
  controller?.abort()
  const current = new AbortController()
  controller = current
  const query = { ...filters }
  loading.value = true
  error.value = ''
  try {
    const result = await getChannelStatistics(query, current.signal)
    if (current.signal.aborted) return
    data.value = result
    applied.value = query
  } catch {
    if (!current.signal.aborted) error.value = text('loadError')
  } finally {
    if (controller === current) loading.value = false
  }
}
function setRange(days: number, offset: number) {
  filters.start_date = beijingDate(offset - days + 1)
  filters.end_date = beijingDate(offset)
  void load()
}
function exportCSV() {
  if (!data.value) return
  const metadata = { start_date: data.value.start_date, end_date: data.value.end_date, timezone: data.value.timezone, generated_at: data.value.generated_at, group_id: applied.value.group_id, model: applied.value.model, effort: applied.value.effort, view: view.value }
  const records = rows.value.map(row => ({ ...metadata, ...row }))
  if (!records.length) return
  const keys = Object.keys(records[0]!) as (keyof typeof records[number])[]
  const escape = (value: unknown) => {
    let cell = value == null ? '' : String(value)
    if (typeof value === 'string' && /^[=+\-@\t\r]/.test(cell)) cell = `'${cell}`
    return `"${cell.replace(/"/g, '""')}"`
  }
  const csv = [keys.map(escape).join(','), ...records.map(row => keys.map(key => escape(row[key])).join(','))].join('\r\n')
  const url = URL.createObjectURL(new Blob(['\uFEFF', csv], { type: 'text/csv;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `channel-statistics-${data.value.start_date}-${data.value.end_date}-${view.value}.csv`
  link.click()
  URL.revokeObjectURL(url)
}
onMounted(() => {
  void load()
  void getAllIncludingInactive().then(result => { groups.value = result }).catch(() => { error.value = text('loadError') })
})
onUnmounted(() => controller?.abort())
</script>

<style scoped>
th, td { padding: 1rem; }
.metric-label { @apply text-sm text-gray-500 dark:text-gray-400; }
.metric-value { @apply mt-2 text-2xl font-semibold tracking-tight tabular-nums; }
.metric-detail { @apply mt-1 text-xs text-gray-500 dark:text-gray-400; }
</style>
