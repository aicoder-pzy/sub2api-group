<template>
  <article
    class="relative group text-left p-5 rounded-2xl min-h-[280px] min-w-0 w-full bg-white/70 backdrop-blur-xl border border-gray-200/80 shadow-card dark:bg-dark-800/60 dark:border-dark-700/70 hover:-translate-y-1 hover:shadow-card-hover dark:hover:border-primary-500/30 hover:border-gray-300 transition-all duration-300 ease-out flex flex-col"
    @click="emit('click')"
  >
    <button type="button" class="absolute inset-0 rounded-2xl" :aria-label="`${modelName} (${item.name})`" />
    <div class="relative w-full">
      <!-- Probe model, channel and latest status -->
      <div class="flex items-start gap-3">
        <span
          class="w-9 h-9 rounded-xl ring-1 ring-black/5 dark:ring-white/10 grid place-items-center flex-shrink-0"
          :class="[providerGradient(item.provider), providerTintClass]"
        >
          <ProviderIcon :provider="item.provider" :size="20" />
        </span>
        <div class="flex-1 min-w-0">
          <h3 class="break-all font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">
            {{ modelName }}
          </h3>
          <div class="mt-0.5 break-words text-sm text-gray-600 dark:text-gray-300">{{ item.name }}</div>
          <div class="mt-1 flex flex-wrap items-center gap-1.5">
            <span
              class="inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium"
              :class="providerBadgeClass(item.provider)"
            >
              {{ providerLabel(item.provider) }}
            </span>
            <span
              v-if="item.group_name"
              class="break-all rounded-md px-1.5 py-0.5 text-[10px] font-medium bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300"
            >
              {{ item.group_name }}
            </span>
          </div>
        </div>
        <span
          class="px-2.5 py-1 rounded-full text-xs font-semibold flex-shrink-0"
          :class="statusBadgeClass(status)"
        >
          {{ statusLabel(status) }}
        </span>
      </div>

      <!-- Metrics -->
      <MonitorMetricPair
        primary-icon="bolt"
        :primary-label="t('monitorCommon.dialogLatency')"
        :primary-value="formatLatency(probe ? probe.latency_ms : item.primary_latency_ms)"
        primary-unit="ms"
        secondary-icon="globe"
        :secondary-label="t('monitorCommon.endpointPing')"
        :secondary-value="formatLatency(probe ? probe.ping_latency_ms : item.primary_ping_latency_ms)"
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
      />

      <!-- Timeline -->
      <MonitorTimeline
        :buckets="probe ? probe.timeline : item.timeline"
        :countdown-seconds="countdownSeconds"
      />
    </div>
    <MonitorIntelligenceSummary v-if="!probe && item.intelligence" :intelligence="item.intelligence" :model="item.primary_model"
      class="relative mt-4 w-full border-t border-gray-200 pt-3 dark:border-dark-700" />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserMonitorExtraModel, UserMonitorView } from '@/api/channelMonitor'
import {
  useChannelMonitorFormat,
  providerGradient,
} from '@/composables/useChannelMonitorFormat'
import { isChannelMonitorQuotaVisible } from '@/utils/featureFlags'
import ProviderIcon from './ProviderIcon.vue'
import MonitorMetricPair from './MonitorMetricPair.vue'
import MonitorAvailabilityRow from './MonitorAvailabilityRow.vue'
import MonitorTimeline from './MonitorTimeline.vue'
import MonitorIntelligenceSummary from './MonitorIntelligenceSummary.vue'
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
  probe?: UserMonitorExtraModel
  window: '7d' | '15d' | '30d'
  availabilityValue: number | null
  countdownSeconds: number
}>()

const emit = defineEmits<{
  (e: 'click'): void
}>()

const { t } = useI18n()
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

const modelName = computed(() => formatMonitorModel(props.probe?.model ?? props.item.primary_model))
const status = computed(() => props.probe?.status ?? props.item.primary_status)
const quotaVisible = computed(
  () => !props.probe && isChannelMonitorQuotaVisible() && !!props.item.latest_quota
)

const availabilityLabel = computed(() => {
  const win = t(`channelStatus.windowTab.${props.window}`)
  return `${t('monitorCommon.availabilityPrefix')} · ${win}`
})

</script>
