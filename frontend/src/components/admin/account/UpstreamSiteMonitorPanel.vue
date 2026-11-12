<template>
  <section class="space-y-4 p-5" data-site-monitor :aria-busy="loading">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="max-w-2xl text-sm leading-6 text-gray-500"><p>{{ tr('readOnly') }}</p><p class="mt-1 text-xs">{{ tr(snapshot?.mode === 'v2' ? 'v2Hint' : 'v1Hint') }}</p></div>
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />{{ t('upstreamSites.refresh') }}</button>
    </div>
    <div class="flex flex-wrap gap-x-5 gap-y-1 text-xs text-gray-500">
      <a v-if="snapshot?.source_url" :href="snapshot.source_url" target="_blank" rel="noopener noreferrer" class="text-primary-600 underline dark:text-primary-400">{{ tr('source') }}</a>
      <span>{{ tr('receivedAt') }}：{{ formatTime(snapshot?.received_at) }}</span>
      <span>{{ tr('dataAt') }}：{{ formatTime(snapshot?.data_at) }}</span>
      <span>{{ tr('refreshInterval', { seconds: snapshot?.refresh_seconds || 60 }) }}</span>
    </div>
    <p v-if="error || snapshot?.status === 'error'" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ tr('readFailed') }}<span v-if="snapshot?.rows.length"> {{ tr('retained') }}</span></p>
    <p v-if="snapshot?.status === 'disabled' || snapshot?.status === 'unsupported'" role="status" class="py-8 text-center text-sm text-gray-500">{{ tr(snapshot.status) }}</p>
    <template v-else-if="snapshot && (snapshot.status === 'ok' || snapshot.rows.length)">
      <div v-if="snapshot.status === 'ok' && unmatchedGroups.length" class="rounded-lg bg-gray-50 p-3 text-xs leading-6 text-gray-600 dark:bg-dark-800 dark:text-gray-400" data-monitor-unmatched>
        <p>{{ tr('unmatched') }}</p><p class="mt-1 break-words">{{ unmatchedGroups.map(group => group.name || '#' + group.id).join('、') }}</p>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <select v-model="scope" class="input w-auto max-w-full" :aria-label="tr('scope')"><option value="all">{{ tr('all') }}</option><option value="keys">{{ tr('keyGroups') }}</option></select>
        <div class="flex flex-wrap items-center gap-3 text-xs text-gray-500"><span>{{ tr('rows', { count: rows.length }) }}</span><span class="rounded-full bg-gray-100 px-2 py-1 dark:bg-dark-700">{{ snapshot.bucket_seconds ? tr('bucketMinutes', { minutes: snapshot.bucket_seconds / 60 }) : tr('perCheck') }}</span><button type="button" class="btn btn-secondary !px-2 !py-1 !text-xs" :disabled="!isZoomed(zoom)" @click="zoom = resetZoom()">{{ tr('resetZoom') }}</button></div>
      </div>
      <p class="text-xs text-gray-400">{{ tr('trendHint') }}</p>
      <div class="max-h-[min(56vh,560px)] overflow-auto rounded-xl" tabindex="0" :aria-label="t('upstreamSites.monitoring')">
        <table class="monitor-table">
          <thead><tr><th>{{ tr('channel') }}</th><th>{{ tr(snapshot.mode === 'v2' ? 'successRate' : 'availability') }}</th><th>{{ tr(snapshot.mode === 'v2' ? 'ttft' : 'latency') }}</th><th>{{ tr('cache') }}</th><th class="w-1/2">{{ tr('trend') }}</th></tr></thead>
          <tbody>
            <tr v-for="row in rows" :key="row.id" :data-monitor-row="row.id">
              <td class="min-w-48 max-w-[260px]"><div class="flex items-center gap-2"><strong class="min-w-0 flex-1 truncate font-medium" :title="row.name || row.group_name || row.id">{{ row.name || row.group_name || row.id }}</strong><span class="monitor-state shrink-0" :class="effectiveState(row)" :title="tr('dataAt') + '：' + formatTime(row.checked_at)">{{ tr(effectiveState(row)) }}</span></div><p v-if="row.model" class="mt-1 max-w-52 truncate text-xs text-gray-500" :title="row.model">{{ row.model }}</p><p v-if="row.group_name && row.group_name !== row.name" class="mt-1 max-w-52 truncate text-xs text-gray-500" :title="row.group_name">{{ row.group_name }}</p><span v-if="isKeyGroup(row)" class="mt-1 inline-block text-[10px] text-primary-600 dark:text-primary-400">{{ tr('keyGroup') }}</span><small v-if="effectiveState(row) !== row.state" class="mt-1 block text-[10px] text-gray-500">{{ tr('reported') }}：{{ tr(row.state) }}</small></td>
              <td class="whitespace-nowrap tabular-nums">{{ percent(snapshot.mode === 'v2' ? row.success_rate : row.availability_7d) }}<small v-if="snapshot.mode === 'v2' && row.error_rate != null" class="mt-1 block text-[10px] text-gray-400">{{ tr('errorRate') }} {{ percent(row.error_rate) }}</small></td>
              <td class="tabular-nums">{{ row.latency_ms == null ? '—' : (row.latency_ms / 1000).toFixed(2) + ' s' }}</td>
              <td class="tabular-nums">{{ percent(row.cache_rate) }}<small v-if="row.cache_window_hours" class="mt-1 block text-xs text-gray-500">{{ tr('windowHours', { hours: row.cache_window_hours }) }}</small></td>
              <td><UpstreamMonitorTrend :row="row" :snapshot="snapshot" :zoom="zoom" :stale="['stale', 'undated'].includes(effectiveState(row))" @zoom="(event, ratio) => { zoom = applyWheelZoom(zoom, event, ratio) }" /></td>
            </tr>
            <tr v-if="!rows.length"><td colspan="5" class="py-10 text-center text-gray-500">{{ tr(scope === 'keys' ? 'noMatched' : 'empty') }}</td></tr>
          </tbody>
        </table>
      </div>
      <div class="space-y-2 text-[11px] text-gray-500"><div class="flex items-center gap-2"><span>{{ tr('worse') }}</span><div class="trend-legend h-2.5 flex-1 rounded-full"></div><span>{{ tr('better') }}</span></div><div class="flex flex-wrap gap-4"><span v-for="state in ['healthy', 'warning', 'critical', 'unknown']" :key="state" class="flex items-center gap-1.5"><i class="legend-dot" :class="state"></i>{{ tr(state === 'unknown' ? 'noBucket' : state) }}</span></div></div>
      <p class="text-xs leading-5 text-gray-500">{{ tr('mappingHint') }}</p>
    </template>
    <p v-else-if="loading" role="status" class="py-8 text-center text-sm text-gray-500">{{ t('upstreamSites.loading') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import UpstreamMonitorTrend from './UpstreamMonitorTrend.vue'
import { applyWheelZoom, isZoomed, resetZoom } from '@/features/channel-monitor-v2/monitorZoom'
import { upstreamSitesAPI, type UpstreamSite, type SiteMonitor, type SiteMonitorRow } from '@/api/admin/upstreamSites'

const props = defineProps<{ site: UpstreamSite }>()
const { t, locale } = useI18n()
const tr = (key: string, values: Record<string, number> = {}) => t(`upstreamSites.monitor.${key}`, values)
const snapshot = ref<SiteMonitor>(), loading = ref(false), error = ref(false), scope = ref('all'), clock = ref(Date.now())
const zoom = ref(resetZoom())
let controller: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined
const keyGroups = computed(() => {
  const groups = new Map<string, { id: string; name: string }>()
  for (const key of props.site.inventory?.keys || []) if (key.group_id) groups.set(key.group_id, { id: key.group_id, name: key.group_name })
  return [...groups.values()]
})
function matchesGroup(row: SiteMonitorRow, group: { id: string; name: string }) {
  if (row.group_id) return row.group_id === group.id
  return !!row.group_name && row.group_name === group.name && props.site.inventory?.groups.filter(item => item.name === group.name).length === 1
}
const isKeyGroup = (row: SiteMonitorRow) => keyGroups.value.some(group => matchesGroup(row, group))
const unmatchedGroups = computed(() => keyGroups.value.filter(group => !snapshot.value?.rows.some(row => matchesGroup(row, group))))
const rows = computed(() => (snapshot.value?.rows || []).filter(row => scope.value === 'all' || isKeyGroup(row)))
function effectiveState(row: SiteMonitorRow) {
  if (error.value || snapshot.value?.stale || snapshot.value?.status === 'error') return 'stale'
  const checkedAt = Date.parse(row.checked_at || '')
  if (!Number.isFinite(checkedAt) || checkedAt > clock.value + 300_000) return 'undated'
  return clock.value - checkedAt > 900_000 ? 'stale' : row.state
}
function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : tr('noTime')
}
const percent = (value: number | null | undefined) => value == null ? '—' : value.toFixed(1) + '%'
async function load() {
  if (loading.value || document.hidden) return
  clearTimeout(timer)
  const request = new AbortController()
  controller = request
  loading.value = true
  try {
    const result = await upstreamSitesAPI.monitoring(props.site.id, request.signal)
    if (!request.signal.aborted) { snapshot.value = result; error.value = false }
  } catch {
    if (!request.signal.aborted) error.value = true
  } finally {
    if (!request.signal.aborted) {
      loading.value = false
      clock.value = Date.now()
      if (!document.hidden) timer = setTimeout(() => { void load() }, Math.max(60, Math.min(snapshot.value?.refresh_seconds || 60, 300)) * 1000)
    }
  }
}
watch([() => props.site.id, () => props.site.revision], () => {
  controller?.abort()
  clearTimeout(timer)
  snapshot.value = undefined; error.value = false; loading.value = false; scope.value = 'all'
  zoom.value = resetZoom()
  void load()
}, { immediate: true })
function onVisibility() {
  clearTimeout(timer)
  clock.value = Date.now()
  if (!document.hidden) void load()
}
document.addEventListener('visibilitychange', onVisibility)
onBeforeUnmount(() => { controller?.abort(); clearTimeout(timer); document.removeEventListener('visibilitychange', onVisibility) })
</script>

<style scoped>
.monitor-table { @apply w-full text-left text-sm; }
.monitor-table th { @apply sticky top-0 z-10 whitespace-nowrap border-b border-gray-200 bg-gray-50 px-3 py-3 text-xs font-medium text-gray-500 dark:border-dark-700 dark:bg-dark-900; }
.monitor-table td { @apply border-b border-gray-100 px-3 py-3 align-middle dark:border-dark-800; }
.monitor-state { @apply inline-block whitespace-nowrap rounded-md bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600 dark:bg-dark-700 dark:text-gray-300; }
.monitor-state.healthy { @apply bg-green-50 text-green-700 dark:bg-green-950/30 dark:text-green-300; }
.monitor-state.warning { @apply bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300; }
.monitor-state.critical { @apply bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300; }
.trend-legend { background: linear-gradient(90deg, #ef4343, #f97316, #facc15, #a3e635, #22c55e, #16a34a); }
.legend-dot { @apply h-2 w-2 rounded-full bg-gray-400; }.legend-dot.healthy { background: #22c55e; }.legend-dot.warning { background: #f59e0b; }.legend-dot.critical { background: #ef4444; }
</style>
