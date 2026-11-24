<template>
  <div>
    <div
      v-if="loading && items.length === 0"
      class="grid items-start gap-5 grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
    >
      <div
        v-for="i in 6"
        :key="i"
        class="p-5 rounded-2xl min-h-[280px] bg-white/70 dark:bg-dark-800/60 border border-gray-200/80 dark:border-dark-700/70 animate-pulse"
      >
        <div class="flex items-start gap-3">
          <div class="w-9 h-9 rounded-xl bg-gray-200 dark:bg-dark-700"></div>
          <div class="flex-1 space-y-2">
            <div class="h-4 w-2/3 rounded bg-gray-200 dark:bg-dark-700"></div>
            <div class="h-3 w-1/2 rounded bg-gray-200 dark:bg-dark-700"></div>
          </div>
          <div class="h-6 w-16 rounded-full bg-gray-200 dark:bg-dark-700"></div>
        </div>
        <div class="mt-5 grid grid-cols-2 gap-2">
          <div class="h-16 rounded-xl bg-gray-100 dark:bg-dark-900/40"></div>
          <div class="h-16 rounded-xl bg-gray-100 dark:bg-dark-900/40"></div>
        </div>
        <div class="mt-6 h-5 w-full rounded bg-gray-100 dark:bg-dark-900/40"></div>
      </div>
    </div>

    <EmptyState
      v-else-if="items.length === 0"
      :title="t('channelStatus.empty.title')"
      :description="t('channelStatus.empty.description')"
    />

    <div
      v-else
      class="grid items-start gap-5 grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
    >
      <MonitorCard
        v-for="item in items"
        :key="item.id"
        :item="item"
        :window="window"
        :availability-values="resolveAvailabilities(item)"
        :countdown-seconds="countdownSeconds"
        @click="model => emit('cardClick', item, model)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { UserMonitorView, UserMonitorDetail, UserMonitorExtraModel } from '@/api/channelMonitor'
import EmptyState from '@/components/common/EmptyState.vue'
import MonitorCard from './MonitorCard.vue'

const props = defineProps<{
  items: UserMonitorView[]
  window: '7d' | '15d' | '30d'
  countdownSeconds: number
  loading: boolean
  detailCache: Record<number, UserMonitorDetail>
}>()

const emit = defineEmits<{
  (e: 'cardClick', item: UserMonitorView, model: string): void
}>()

const { t } = useI18n()

function resolveAvailabilities(item: UserMonitorView): Record<string, number | null> {
  return Object.fromEntries([
    [item.primary_model, resolveAvailability(item)],
    ...(item.extra_models ?? []).map(model => [model.model, resolveAvailability(item, model)]),
  ])
}

function resolveAvailability(item: UserMonitorView, probe?: UserMonitorExtraModel): number | null {
  if (props.window === '7d') {
    if (probe) return probe.availability_7d ?? null
    return item.has_availability_7d === false || !item.primary_status ? null : item.availability_7d ?? null
  }
  if (!(probe ? probe.status : item.primary_status)) return null
  const model = probe?.model ?? item.primary_model
  const stats = props.detailCache[item.id]?.models.find(entry => entry.model === model)
  return props.window === '15d' ? stats?.availability_15d ?? null : stats?.availability_30d ?? null
}
</script>
