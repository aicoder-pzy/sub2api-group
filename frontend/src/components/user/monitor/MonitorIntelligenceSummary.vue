<template>
  <section class="space-y-3 text-xs" data-testid="monitor-intelligence">
    <h3 v-if="!hideTitle" class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelStatus.intelligence.title') }}</h3>
    <div v-for="test in questions" :key="test.id" :data-testid="`monitor-intelligence-test-${test.id}`">
      <div class="flex items-center justify-between gap-2">
        <span v-if="test.kind" class="min-w-0 break-words text-gray-700 dark:text-gray-200">{{ questionLabel(test) }}</span>
        <span class="flex shrink-0 items-center gap-1.5 text-gray-600 dark:text-gray-300">
          <span class="h-2 w-2 rounded-full" :class="intelligenceColor(test.state)" />
          {{ intelligenceLabel(test.state) }}
        </span>
      </div>
      <div v-if="test.history.length" class="mt-1.5 flex gap-1">
        <button v-for="(point, index) in test.history" :key="index" type="button"
          class="h-3 min-w-0 flex-1 rounded-sm focus-visible:outline focus-visible:outline-2" :class="intelligenceColor(point.status)"
          :title="intelligenceDetail(point)" :aria-label="intelligenceDetail(point)"
          @click.stop="selected = { testId: test.id, point }"
        />
      </div>
      <p v-if="test.history.length" class="mt-1.5 break-words text-gray-500 dark:text-gray-400">{{ intelligenceDetail(selected?.testId === test.id ? selected.point : test.history[test.history.length - 1]) }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorIntelligence, MonitorIntelligencePoint, MonitorIntelligenceTest } from '@/api/channelMonitor'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'

const props = defineProps<{ intelligence: MonitorIntelligence; model: string; hideTitle?: boolean }>()
const { t } = useI18n()
const { formatLatency } = useChannelMonitorFormat()
const selected = ref<{ testId: number; point: MonitorIntelligencePoint }>()
watch(() => props.intelligence, () => { selected.value = undefined })
const questions = computed<MonitorIntelligenceTest[]>(() => props.intelligence.tests?.length ? props.intelligence.tests : [
  { id: 0, kind: '', state: props.intelligence.state, history: props.intelligence.history },
])
const colors: Record<string, string> = {
  normal: 'bg-emerald-500',
  degraded: 'bg-amber-400',
  incorrect: 'bg-rose-500',
  failed: 'bg-rose-500',
}
function intelligenceColor(status: string) {
  return colors[status] ?? 'bg-gray-300 dark:bg-dark-500'
}
function intelligenceLabel(status: string) {
  return t(`channelStatus.intelligence.${status}`)
}
function questionLabel(test: MonitorIntelligenceTest) {
  if (test.kind === 'candy') return t('admin.accounts.pelicanTest.candyQuestion')
  if (test.kind === 'custom') return test.name || t('pelicanTests.editor.customQuestion')
  return t('pelicanTests.pelicanQuestion')
}
function intelligenceDetail(point: MonitorIntelligencePoint) {
  return [new Date(point.checked_at).toLocaleString(), props.model, point.effort,
    intelligenceLabel(point.status), point.latency_ms > 0 ? `${formatLatency(point.latency_ms)} ms` : ''].filter(Boolean).join(' · ')
}
</script>
