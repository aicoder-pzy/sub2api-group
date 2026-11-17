<template>
  <div class="space-y-3">
    <div class="flex flex-wrap items-center justify-between gap-2"><span class="input-label !mb-0">{{ t('upstreamSites.upstreamGroup') }}</span><button v-if="site" type="button" class="text-xs text-primary-600" :disabled="loading" @click="load">{{ t(loading ? 'upstreamSites.loading' : 'upstreamSites.refreshGroupStatus') }}</button></div>
    <input v-model="search" type="search" class="input" :placeholder="t('upstreamSites.searchGroups')" :aria-label="t('upstreamSites.searchGroups')" />
    <p class="text-xs leading-5 text-gray-500">{{ t('upstreamSites.groupStatusHint') }}</p>
    <p v-if="failed || snapshot?.status === 'error'" role="status" class="text-xs text-amber-700 dark:text-amber-400">{{ t('upstreamSites.monitor.readFailed') }}</p>
    <fieldset class="max-h-80 space-y-2 overflow-y-auto" :aria-label="t('upstreamSites.upstreamGroup')">
      <label v-for="group in visibleGroups" :key="group.id" class="group-option" :class="{ selected: modelValue === group.id }" :data-group-option="group.id">
        <input type="radio" :name="radioName" :value="group.id" :checked="modelValue === group.id" :disabled="disabled" :aria-label="group.name" @change="emit('update:modelValue', group.id)" />
        <span class="min-w-0 flex-1">
          <span class="flex flex-wrap items-center justify-between gap-2"><strong class="break-words text-sm font-medium">{{ group.name }}</strong><span class="group-state" :class="state(group)" :title="statusDetails(group)">{{ stateLabel(group) }}</span></span>
          <span class="mt-1 block text-xs text-gray-500">{{ group.multiplier === undefined ? t('upstreamSites.unknownGroupRate') : '×' + group.multiplier }}<span v-if="group.status"> · {{ t('upstreamSites.groupEnabledState') }}：{{ enabledLabel(group.status) }}</span></span>
          <span v-if="matching(group).length" class="mt-1 block text-[11px] text-gray-500">{{ t('upstreamSites.groupMonitorCount', { count: matching(group).length }) }} · {{ t('upstreamSites.updated') }}：{{ checkedTime(group) }}</span>
        </span>
      </label>
      <p v-if="!visibleGroups.length" class="py-4 text-center text-sm text-gray-500">{{ t('upstreamSites.noMatch') }}</p>
    </fieldset>
    <section v-if="selected" data-upstream-group-detail class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800">
      <div class="flex flex-wrap items-center justify-between gap-2 text-sm font-medium">
        <span class="break-words">{{ selected.name }}</span>
        <span class="shrink-0 text-primary-600 dark:text-primary-400">{{ t('upstreamSites.groupMultiplier') }}：{{ selected.multiplier === undefined ? t('upstreamSites.unknownGroupRate') : '×' + selected.multiplier }}</span>
      </div>
      <p class="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-gray-600 dark:text-gray-300">{{ selected.description || t('upstreamSites.noGroupDescription') }}</p>
      <ul v-if="matching(selected).length" class="mt-3 space-y-2 border-t border-gray-200 pt-3 text-xs dark:border-dark-600"><li v-for="row in matching(selected)" :key="row.id" class="flex flex-wrap justify-between gap-2"><span>{{ row.model || row.name }}</span><span>{{ t('upstreamSites.monitor.' + rowState(row)) }}<template v-if="row.success_rate != null"> · {{ t('upstreamSites.monitor.successRate') }} {{ row.success_rate.toFixed(1) }}%</template><template v-else-if="row.availability_7d != null"> · {{ t('upstreamSites.monitor.availability') }} {{ row.availability_7d.toFixed(1) }}%</template><template v-if="row.latency_ms != null"> · {{ t('upstreamSites.monitor.' + (row.latency_kind === 'ttft_p50' ? 'ttft' : 'latency')) }} {{ (row.latency_ms / 1000).toFixed(2) }} s</template></span></li></ul>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { upstreamSitesAPI, type SiteGroup, type UpstreamSite, type SiteMonitor, type SiteMonitorRow } from '@/api/admin/upstreamSites'

const props = defineProps<{ modelValue: string; groups: SiteGroup[]; disabled?: boolean; site?: UpstreamSite }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t, locale } = useI18n()
const radioName = useId()
const search = ref(''), snapshot = ref<SiteMonitor>(), loading = ref(false), failed = ref(false), clock = ref(Date.now())
let controller: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined
const selected = computed(() => props.groups.find(group => group.id === props.modelValue))
const visibleGroups = computed(() => props.groups.filter(group => `${group.name} ${group.id}`.toLowerCase().includes(search.value.toLowerCase())))
function matching(group: SiteGroup) {
  return (snapshot.value?.rows || []).filter(row => row.group_id ? row.group_id === group.id : row.group_name === group.name && props.groups.filter(item => item.name === group.name).length === 1)
}
function rowState(row: SiteMonitorRow) {
  if (failed.value || snapshot.value?.stale || snapshot.value?.status === 'error') return 'stale'
  const at = Date.parse(row.checked_at || '')
  if (!Number.isFinite(at) || at > clock.value + 300_000) return 'undated'
  return clock.value - at > 900_000 ? 'stale' : row.state
}
function state(group: SiteGroup) {
  const states = matching(group).map(rowState)
  if (!states.length) return 'unreported'
  // Show the worst reported model; partial or old data never appears all green.
  return ['critical', 'warning', 'stale', 'undated', 'unknown', 'healthy'].find(value => states.includes(value as ReturnType<typeof rowState>)) || 'unknown'
}
const stateLabel = (group: SiteGroup) => loading.value && !snapshot.value ? t('upstreamSites.loading') : state(group) === 'unreported' ? t('upstreamSites.noGroupStatus') : t('upstreamSites.monitor.' + state(group))
const enabledLabel = (value: string) => ['active', 'enabled', '1'].includes(value) ? t('upstreamSites.groupEnabled') : ['inactive', 'disabled', '0', '2'].includes(value) ? t('upstreamSites.groupDisabled') : value
const statusDetails = (group: SiteGroup) => matching(group).map(row => `${row.model || row.name}: ${t('upstreamSites.monitor.' + rowState(row))}`).join('\n')
function checkedTime(group: SiteGroup) {
  const times = matching(group).map(row => Date.parse(row.checked_at || '')).filter(Number.isFinite)
  return times.length ? new Date(Math.min(...times)).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US') : t('upstreamSites.monitor.noTime')
}
async function load() {
  if (!props.site || loading.value || document.hidden) return
  clearTimeout(timer)
  const request = new AbortController(); controller = request; loading.value = true
  try {
    const result = await upstreamSitesAPI.monitoring(props.site.id, request.signal)
    if (!request.signal.aborted) { snapshot.value = result; failed.value = false }
  } catch { if (!request.signal.aborted) failed.value = true }
  finally {
    if (!request.signal.aborted) {
      loading.value = false; clock.value = Date.now()
      timer = setTimeout(() => { void load() }, Math.max(60, Math.min(snapshot.value?.refresh_seconds || 60, 300)) * 1000)
    }
  }
}
watch([() => props.site?.id, () => props.site?.revision], () => {
  controller?.abort(); clearTimeout(timer); snapshot.value = undefined; failed.value = false; loading.value = false; search.value = ''; void load()
}, { immediate: true })
function onVisibility() { clearTimeout(timer); clock.value = Date.now(); if (!document.hidden) void load() }
document.addEventListener('visibilitychange', onVisibility)
onBeforeUnmount(() => { controller?.abort(); clearTimeout(timer); document.removeEventListener('visibilitychange', onVisibility) })
</script>

<style scoped>
.group-option { @apply flex cursor-pointer items-start gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600; }
.group-option.selected { @apply border-primary-500 bg-primary-50 dark:bg-primary-950/30; }
.group-option input { @apply mt-1; }
.group-state { @apply whitespace-nowrap rounded-md bg-gray-100 px-2 py-1 text-[11px] text-gray-500 dark:bg-dark-700; }
.group-state.healthy { @apply bg-green-50 text-green-700 dark:bg-green-950/30 dark:text-green-300; }
.group-state.warning, .group-state.stale { @apply bg-amber-50 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300; }
.group-state.critical { @apply bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300; }
</style>
