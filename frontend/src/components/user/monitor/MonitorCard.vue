<template>
  <article
    class="relative group text-left p-5 rounded-2xl min-h-[280px] min-w-0 w-full bg-white/70 backdrop-blur-xl border border-gray-200/80 shadow-card dark:bg-dark-800/60 dark:border-dark-700/70 hover:-translate-y-1 hover:shadow-card-hover dark:hover:border-primary-500/30 hover:border-gray-300 transition-all duration-300 ease-out grid row-span-3 grid-rows-subgrid gap-y-0"
  >
    <div class="contents">
      <!-- One card per channel; switch models without stacking result panels. -->
      <div class="flex items-start gap-3">
        <span
          class="w-9 h-9 rounded-xl ring-1 ring-black/5 dark:ring-white/10 grid place-items-center flex-shrink-0"
          :class="[providerGradient(item.provider), providerTintClass]"
        >
          <ProviderIcon :provider="item.provider" :size="20" />
        </span>
        <div class="flex-1 min-w-0">
          <h3 class="break-words text-base font-semibold text-gray-900 dark:text-gray-100">
            {{ item.name }}
          </h3>
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
      </div>

      <div class="mt-3 flex items-start gap-2">
        <div class="flex min-w-0 flex-1 flex-wrap gap-1.5">
          <button
            v-for="probe in models"
            :key="probe.model"
            type="button"
            :data-model-select="probe.model"
            :aria-pressed="model.model === probe.model"
            :title="statusLabel(probe.status)"
            class="max-w-full rounded-md border border-transparent px-2 py-1 text-left text-xs focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
            :class="[statusBadgeClass(probe.status), { 'ring-1 ring-current': model.model === probe.model }]"
            @click="selectedModel = probe.model"
          >
            <span class="break-all font-mono font-medium">{{ formatMonitorModel(probe.model) }}</span>
            <span class="sr-only"> · {{ statusLabel(probe.status) }}</span>
          </button>
        </div>
        <button
          type="button"
          class="shrink-0 rounded-md px-1 py-1 text-xs text-gray-500 hover:text-primary-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-gray-400"
          :aria-label="`${formatMonitorModel(model.model)} (${item.name})`"
          @click="emit('click', model.model)"
        >
          {{ t('common.view') }} ›
        </button>
      </div>

      <section :data-model="model.model" :aria-label="formatMonitorModel(model.model)">
        <MonitorMetricPair
          primary-icon="bolt"
          :primary-label="t('monitorCommon.dialogLatency')"
          :primary-value="formatLatency(model.latency_ms)"
          primary-unit="ms"
          secondary-icon="globe"
          :secondary-label="t('monitorCommon.endpointPing')"
          :secondary-value="formatLatency(model.ping_latency_ms)"
          secondary-unit="ms"
        />

        <MonitorQuotaView
          v-if="model.model === item.primary_model && quotaVisible"
          :snapshot="item.latest_quota"
          class="mt-2"
        />

        <MonitorAvailabilityRow
          :window-label="availabilityLabel"
          :value="availabilityValues[model.model] ?? null"
        />

        <MonitorTimeline
          :buckets="model.timeline"
          :countdown-seconds="countdownSeconds"
        />

        <MonitorIntelligenceSummary
          v-if="model.model === item.primary_model && item.intelligence"
          :intelligence="item.intelligence"
          :model="item.primary_model"
          class="mt-4 w-full border-t border-gray-200 pt-3 dark:border-dark-700"
        />
      </section>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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
  window: '7d' | '15d' | '30d'
  availabilityValues: Record<string, number | null>
  countdownSeconds: number
}>()

const emit = defineEmits<{
  (e: 'click', model: string): void
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

const models = computed<UserMonitorExtraModel[]>(() => [
  {
    model: props.item.primary_model,
    status: props.item.primary_status,
    latency_ms: props.item.primary_latency_ms,
    ping_latency_ms: props.item.primary_ping_latency_ms,
    timeline: props.item.timeline,
  },
  ...(props.item.extra_models ?? []).filter(model => model.model && model.model !== props.item.primary_model),
])

const selectedModel = ref(props.item.primary_model)
const model = computed(() => models.value.find(probe => probe.model === selectedModel.value) ?? models.value[0]!)
watch(models, probes => {
  if (!probes.some(probe => probe.model === selectedModel.value)) selectedModel.value = props.item.primary_model
})

const quotaVisible = computed(
  () => isChannelMonitorQuotaVisible() && !!props.item.latest_quota
)

const availabilityLabel = computed(() => {
  const win = t(`channelStatus.windowTab.${props.window}`)
  return `${t('monitorCommon.availabilityPrefix')} · ${win}`
})
</script>
