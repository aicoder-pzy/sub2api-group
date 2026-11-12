<template>
  <div class="min-w-[320px]" data-monitor-trend>
    <template v-if="slots.length">
      <div class="mb-2 flex justify-between text-[10px] tabular-nums text-gray-400"><span>{{ time(slots[0]!.at) }}</span><span>{{ time(slots[slots.length - 1]!.at) }}</span></div>
      <div class="flex h-5 gap-[3px]" :class="{ 'opacity-50 grayscale': stale }" @wheel.prevent="wheel">
        <button v-for="slot in slots" :key="slot.at" type="button" class="trend-cell" :class="color(slot.point)" :title="details(slot).join('\n')" :aria-label="details(slot).join('\n')" @mouseenter="show($event, slot)" @mouseleave="active = undefined" @focus="show($event, slot)" @blur="active = undefined" @click="show($event, slot)" @keydown.esc="active = undefined"></button>
      </div>
    </template>
    <p v-else class="rounded-md bg-gray-50 px-3 py-4 text-center text-xs text-gray-400 dark:bg-dark-800">{{ tr('noHistory') }}</p>
    <Teleport to="body">
      <div v-if="active" role="tooltip" class="trend-tooltip" :style="tooltipStyle"><p v-for="(line, index) in details(active)" :key="index" :class="index === 0 ? 'mb-1 font-semibold' : 'text-gray-300'">{{ line }}</p></div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SiteMonitor, SiteMonitorPoint, SiteMonitorRow } from '@/api/admin/upstreamSites'
import { scoreToBand } from '@/features/channel-monitor-v2/monitorFormat'
import { clientXRatio, sliceByZoom, type ZoomState } from '@/features/channel-monitor-v2/monitorZoom'

const props = defineProps<{ row: SiteMonitorRow; snapshot: SiteMonitor; zoom: ZoomState; stale: boolean }>()
const emit = defineEmits<{ zoom: [event: WheelEvent, ratio: number] }>()
const { t, locale } = useI18n()
const tr = (key: string) => t(`upstreamSites.monitor.${key}`)
type Slot = { at: string; point?: SiteMonitorPoint }
const active = ref<Slot>(), tooltipStyle = ref<Record<string, string>>({})
const slots = computed(() => {
  const points = props.row.points || []
  let all: Slot[] = points.map(point => ({ at: point.at, point }))
  const start = Date.parse(props.snapshot.trend_start || ''), end = Date.parse(props.snapshot.trend_end || '')
  const step = (props.snapshot.bucket_seconds || 0) * 1000
  if (step >= 60_000 && end > start && (end - start) / step <= 256) {
    const byTime = new Map(points.map(point => [Date.parse(point.at), point]))
    all = []
    for (let cursor = start; cursor < end; cursor += step) all.push({ at: new Date(cursor).toISOString(), point: byTime.get(cursor) })
  }
  return sliceByZoom(all, props.zoom)
})
function color(point?: SiteMonitorPoint) {
  if (!point || point.state === 'unknown') return 'health-unknown'
  return point.score == null ? `health-${point.state}` : `health-${scoreToBand(point.score)}`
}
function time(value: string) {
  return new Date(value).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
function details(slot: Slot) {
  const end = props.snapshot.bucket_seconds ? new Date(Date.parse(slot.at) + props.snapshot.bucket_seconds * 1000).toISOString() : undefined
  const lines = [time(slot.at) + (end ? ' – ' + time(end) : '')]
  if (props.stale) lines.push(tr('retained'))
  const point = slot.point
  if (!point) return [...lines, tr('noBucket')]
  lines.push(tr(point.state))
  if (point.state !== 'unknown' && point.score != null) lines.push(`${tr('score')}：${point.score.toFixed(1)}`)
  if (point.latency_ms != null) lines.push(`${tr(props.row.latency_kind === 'check' ? 'latency' : 'ttft')}：${(point.latency_ms / 1000).toFixed(2)} s`)
  for (const [key, value] of [['successRate', point.success_rate], ['errorRate', point.error_rate], ['cache', point.cache_rate]] as const) {
    if (value != null) lines.push(`${tr(key)}：${value.toFixed(1)}%`)
  }
  return lines
}
function show(event: MouseEvent | FocusEvent, slot: Slot) {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  const half = Math.min(112, (window.innerWidth - 24) / 2)
  const above = rect.top > 190
  tooltipStyle.value = { left: `${Math.max(half + 8, Math.min(window.innerWidth - half - 8, rect.left + rect.width / 2))}px`, top: `${above ? rect.top - 8 : rect.bottom + 8}px`, transform: `translate(-50%, ${above ? '-100%' : '0'})` }
  active.value = slot
}
function wheel(event: WheelEvent) {
  active.value = undefined
  emit('zoom', event, clientXRatio(event.clientX, event.currentTarget as HTMLElement))
}
watch(() => [props.row, props.zoom, props.stale], () => { active.value = undefined })
</script>

<style scoped>
.trend-cell { @apply min-w-0 flex-1 rounded-[3px] border-0 p-0 outline-offset-2 transition-opacity hover:opacity-75 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500; }
.health-score10 { background: #16a34a; }.health-score9 { background: #22c55e; }.health-score8 { background: #4ade80; }.health-score7 { background: #a3e635; }.health-score6 { background: #facc15; }.health-score5 { background: #fbbf24; }.health-score4 { background: #f59e0b; }.health-score3 { background: #f97316; }.health-score2 { background: #fb7185; }.health-score1 { background: #f87171; }.health-score0 { background: #ef4343; }
.health-healthy { background: #22c55e; }.health-warning { background: #f59e0b; }.health-critical { background: #ef4444; }.health-unknown { background: #9ca3af; }
.trend-tooltip { @apply pointer-events-none fixed z-[9999] w-56 max-w-[calc(100vw-24px)] rounded-xl border border-white/10 bg-gray-900 p-3 text-xs leading-6 text-white shadow-xl dark:bg-dark-900; }
</style>
