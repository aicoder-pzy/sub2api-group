<template>
  <article
    class="relative group text-left p-5 rounded-2xl min-h-[280px] w-full bg-white/70 backdrop-blur-xl border border-gray-200/80 shadow-card dark:bg-dark-800/60 dark:border-dark-700/70 hover:-translate-y-1 hover:shadow-card-hover dark:hover:border-primary-500/30 hover:border-gray-300 transition-all duration-300 ease-out flex flex-col"
    @click="emit('click')"
  >
    <button type="button" class="absolute inset-0 rounded-2xl" :aria-label="item.name" />
    <div class="relative w-full">
      <!-- Header: icon + name/model + status chip -->
      <div class="flex items-start gap-3">
        <span
          class="w-9 h-9 rounded-xl ring-1 ring-black/5 dark:ring-white/10 grid place-items-center flex-shrink-0"
          :class="[providerGradient(item.provider), providerTintClass]"
        >
          <ProviderIcon :provider="item.provider" :size="20" />
        </span>
        <div class="flex-1 min-w-0">
          <div class="text-base font-semibold truncate text-gray-900 dark:text-gray-100">
            {{ item.name }}
          </div>
          <div class="mt-0.5 flex items-center gap-1.5 min-w-0">
            <span
              class="inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium flex-shrink-0"
              :class="providerBadgeClass(item.provider)"
            >
              {{ providerLabel(item.provider) }}
            </span>
            <!-- 纯配额模式主模型是占位符 "quota"，展示层替换为本地化「配额」标签 -->
            <span class="font-mono text-xs truncate text-gray-500 dark:text-gray-400">
              {{ formatMonitorModel(item.primary_model) }}
            </span>
            <span
              v-if="item.group_name"
              class="inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300 flex-shrink-0"
            >
              {{ item.group_name }}
            </span>
          </div>
        </div>
        <span
          class="px-2.5 py-1 rounded-full text-xs font-semibold flex-shrink-0"
          :class="statusBadgeClass(item.primary_status)"
        >
          {{ statusLabel(item.primary_status) }}
        </span>
      </div>

      <!-- Metrics -->
      <MonitorMetricPair
        primary-icon="bolt"
        :primary-label="t('monitorCommon.dialogLatency')"
        :primary-value="formatLatency(item.primary_latency_ms)"
        primary-unit="ms"
        secondary-icon="globe"
        :secondary-label="t('monitorCommon.endpointPing')"
        :secondary-value="formatLatency(item.primary_ping_latency_ms)"
        secondary-unit="ms"
      />

      <!-- 配额模式：最新用量/余额快照（服务端已按系统开关剥离，此处 flag 为纵深防御） -->
      <MonitorQuotaView v-if="quotaVisible" :snapshot="item.latest_quota" class="mt-2" />

      <!-- Divider -->
      <div class="mt-4 border-t border-gray-100 dark:border-dark-700/60"></div>

      <!-- Availability row -->
      <MonitorAvailabilityRow
        :window-label="availabilityLabel"
        :value="availabilityValue"
        :samples-label="extraModelsCountLabel"
      />

      <!-- Timeline -->
      <MonitorTimeline
        :buckets="item.timeline"
        :countdown-seconds="countdownSeconds"
      />
    </div>
    <section v-if="item.intelligence" class="relative mt-4 w-full border-t border-gray-200 pt-3 dark:border-dark-700">
      <div class="flex items-center justify-between gap-2 text-xs">
        <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelStatus.intelligence.title') }}</span>
        <span class="flex items-center gap-1.5 text-gray-600 dark:text-gray-300">
          <span class="h-2 w-2 rounded-full" :class="intelligenceColor(item.intelligence.state)" />
          {{ intelligenceLabel(item.intelligence.state) }}
        </span>
      </div>
      <template v-if="item.intelligence.history.length">
        <div class="mt-2 flex gap-1">
          <button v-for="point in item.intelligence.history" :key="`${point.checked_at}-${point.status}`" type="button"
            class="h-4 min-w-0 flex-1 rounded-sm" :class="intelligenceColor(point.status)"
            :title="intelligenceDetail(point)" :aria-label="intelligenceDetail(point)"
            @click.stop="selectedCheck = point"
          />
        </div>
        <p v-if="displayedCheck" class="mt-2 break-words text-xs text-gray-500 dark:text-gray-400">{{ intelligenceDetail(displayedCheck) }}</p>
      </template>
    </section>
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorIntelligencePoint, UserMonitorView } from '@/api/channelMonitor'
import {
  useChannelMonitorFormat,
  providerGradient,
} from '@/composables/useChannelMonitorFormat'
import { isChannelMonitorQuotaVisible } from '@/utils/featureFlags'
import ProviderIcon from './ProviderIcon.vue'
import MonitorMetricPair from './MonitorMetricPair.vue'
import MonitorAvailabilityRow from './MonitorAvailabilityRow.vue'
import MonitorTimeline from './MonitorTimeline.vue'
import MonitorQuotaView from '@/components/common/MonitorQuotaView.vue'

// 图标配色与 utils/platformColors.ts 的平台色对齐（新 4 家）。
const PROVIDER_TINT: Record<string, string> = {
  openai: 'text-emerald-600 dark:text-emerald-300',
  anthropic: 'text-orange-600 dark:text-orange-300',
  gemini: 'text-sky-600 dark:text-sky-300',
  grok: 'text-zinc-700 dark:text-zinc-200',
  antigravity: 'text-purple-600 dark:text-purple-300',
  kimi: 'text-pink-600 dark:text-pink-300',
  zhipu: 'text-indigo-600 dark:text-indigo-300',
  deepseek: 'text-teal-600 dark:text-teal-300',
  opencode_go: 'text-amber-700 dark:text-amber-300',
}

const props = defineProps<{
  item: UserMonitorView
  window: '7d' | '15d' | '30d'
  availabilityValue: number | null
  countdownSeconds: number
}>()

const emit = defineEmits<{
  (e: 'click'): void
}>()

const { t } = useI18n()
const selectedCheck = ref<MonitorIntelligencePoint>()
watch(() => props.item.intelligence, () => { selectedCheck.value = undefined })
const displayedCheck = computed(() => selectedCheck.value ?? props.item.intelligence?.history.at(-1))
const intelligenceColors: Record<string, string> = {
  normal: 'bg-emerald-500',
  degraded: 'bg-amber-400',
  failed: 'bg-rose-500',
}
function intelligenceColor(status: string) {
  return intelligenceColors[status] ?? 'bg-gray-300 dark:bg-dark-500'
}
function intelligenceLabel(status: string) {
  return t(`channelStatus.intelligence.${status}`)
}
function intelligenceDetail(point: MonitorIntelligencePoint) {
  return [new Date(point.checked_at).toLocaleString(), props.item.primary_model, point.effort,
    intelligenceLabel(point.status), point.latency_ms > 0 ? formatLatency(point.latency_ms) : ''].filter(Boolean).join(' · ')
}
const {
  statusLabel,
  statusBadgeClass,
  providerLabel,
  providerBadgeClass,
  formatLatency,
  formatMonitorModel,
} = useChannelMonitorFormat()

const providerTintClass = computed(() =>
  PROVIDER_TINT[props.item.provider] ?? 'text-gray-500 dark:text-gray-300'
)

const quotaVisible = computed(
  () => isChannelMonitorQuotaVisible() && !!props.item.latest_quota
)

const availabilityLabel = computed(() => {
  const win = t(`channelStatus.windowTab.${props.window}`)
  return `${t('monitorCommon.availabilityPrefix')} · ${win}`
})

const extraModelsCountLabel = computed(() => {
  const count = props.item.extra_models?.length ?? 0
  if (count === 0) return undefined
  return t('monitorCommon.extraModelsCount', { n: count })
})
</script>
