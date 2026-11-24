<template>
  <section class="space-y-3 text-xs" data-testid="monitor-intelligence">
    <h3 v-if="!hideTitle" class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelStatus.intelligence.title') }}</h3>
    <div v-for="test in questions" :key="test.id" class="relative" :data-testid="`monitor-intelligence-test-${test.id}`">
      <div class="flex items-center justify-between gap-2">
        <div v-if="test.kind" class="flex min-w-0 items-center gap-1.5 text-gray-700 dark:text-gray-200">
          <span class="truncate" :title="questionLabel(test)">{{ questionLabel(test) }}</span>
          <details v-if="test.kind === 'candy' && test.history.length" :key="`${test.id}-${currentPoint(test)?.checked_at}`"
            class="shrink-0" data-testid="monitor-candy-answer" @click.stop
            @keydown.esc.stop.prevent="($event.currentTarget as HTMLDetailsElement).open = false">
            <summary class="grid h-4 w-4 cursor-help list-none place-items-center rounded-full border border-gray-400 text-[10px] leading-none text-gray-500 focus-visible:outline focus-visible:outline-2 dark:text-gray-400 [&::-webkit-details-marker]:hidden"
              :aria-label="t('channelStatus.intelligence.actualAnswer')">
              <span aria-hidden="true">i</span>
            </summary>
            <div class="absolute inset-x-0 bottom-full z-30 mb-2 max-h-64 overflow-y-auto rounded-lg border border-gray-200 bg-white p-3 text-gray-700 shadow-lg dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200">
              <div class="mb-2 flex flex-wrap items-center justify-between gap-2 font-medium">
                <span>{{ t('channelStatus.intelligence.actualAnswer') }}</span>
                <span class="text-gray-500 dark:text-gray-400">{{ t('channelStatus.intelligence.expectedAnswer') }}：{{ currentPoint(test)?.expected_answer || '—' }}</span>
              </div>
              <p class="whitespace-pre-wrap break-words" data-testid="monitor-candy-output">{{ currentPoint(test)?.answer?.trim() || t('channelStatus.intelligence.noAnswer') }}</p>
              <p v-if="currentPoint(test)?.answer_truncated" class="mt-1 text-gray-500">{{ t('channelStatus.intelligence.answerTruncated') }}</p>
            </div>
          </details>
        </div>
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
      <p v-if="test.history.length" class="mt-1.5 truncate text-gray-500 dark:text-gray-400"
        data-testid="monitor-intelligence-detail" :title="intelligenceDetail(currentPoint(test)!)">{{ intelligenceDetail(currentPoint(test)!, true) }}</p>
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
function currentPoint(test: MonitorIntelligenceTest) {
  return selected.value?.testId === test.id ? selected.value.point : test.history[test.history.length - 1]
}
function intelligenceDetail(point: MonitorIntelligencePoint, compact = false) {
  const checkedAt = new Date(point.checked_at)
  return [compact ? checkedAt.toLocaleTimeString() : checkedAt.toLocaleString(), compact ? '' : props.model, point.effort,
    intelligenceLabel(point.status), point.latency_ms > 0 ? `${formatLatency(point.latency_ms)} ms` : ''].filter(Boolean).join(' · ')
}
</script>
