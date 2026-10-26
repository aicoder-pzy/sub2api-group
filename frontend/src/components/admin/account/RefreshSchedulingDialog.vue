<template>
  <BaseDialog :show="true" :title="t(readOnly ? 'admin.accounts.schedulingStatus.title' : 'admin.accounts.refreshScheduling.title')" width="extra-wide" @close="close">
    <div class="space-y-4">
      <p class="font-medium">{{ group.name }}</p>
      <p v-if="!readOnly" class="text-sm text-gray-500">{{ t('admin.accounts.refreshScheduling.description') }}</p>
      <p v-if="!readOnly" class="text-sm text-amber-600">{{ t('admin.accounts.refreshScheduling.cost') }}</p>
      <label class="block text-sm">
        {{ t(readOnly ? 'admin.accounts.schedulingStatus.model' : 'admin.accounts.refreshScheduling.model') }}
        <input v-model="model" list="scheduling-models" class="input mt-2 w-full" :disabled="running" maxlength="200" autocomplete="off" @keydown.enter.prevent="readOnly ? loadStatus() : start()" />
        <datalist id="scheduling-models"><option v-for="candidate in models" :key="candidate" :value="candidate" /></datalist>
      </label>
      <label v-if="readOnly && ['quality_preferred', 'model_auto', 'smart_channel'].includes(group.account_scheduling_mode || '')" class="block text-sm">
        {{ t('admin.accounts.schedulingStatus.effort') }}
        <select v-model="effort" class="input mt-2 w-full"><option value="">{{ t('admin.accounts.schedulingStatus.autoEffort') }}</option><option v-for="value in efforts" :key="value" :value="value">{{ value }}</option></select>
      </label>
      <p v-if="!readOnly" class="text-xs text-gray-500">{{ t('admin.accounts.refreshScheduling.policy') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="running" role="status" class="text-sm">{{ t('admin.accounts.refreshScheduling.progress', { done: results.length, total, name: currentName }) }}</p>
      <p v-if="winner" role="status" class="rounded-lg bg-green-50 p-3 text-sm text-green-700 dark:bg-green-900/20">{{ t('admin.accounts.refreshScheduling.complete', { name: winner.name, id: winner.account_id, model: winner.model }) }}</p>
      <div v-if="snapshot" class="space-y-3">
        <p v-if="snapshot.scheduling_mode === 'smart_channel'" class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-950/30 dark:text-blue-200">{{ t('admin.accounts.schedulingStatus.smartCoverage', { declared: snapshot.declared_channels ?? 0, available: snapshot.available_channels ?? 0 }) }} <span v-if="(snapshot.available_channels ?? 0) < 2">{{ t('admin.accounts.schedulingStatus.smartSingleChannel') }}</span></p>
        <template v-if="['model_auto', 'smart_channel'].includes(snapshot.scheduling_mode || '')">
          <p class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-950/30 dark:text-blue-200">{{ t('admin.accounts.schedulingStatus.autoPolicy') }}</p>
          <p v-if="snapshot.model_auto_review" class="text-sm">{{ t('admin.accounts.schedulingStatus.autoReview', { id: snapshot.model_auto_review.candidate_id || '—', count: snapshot.model_auto_review.evaluations, hold: formatDateTime(new Date(snapshot.model_auto_review.hold_until).toISOString()) }) }} · {{ t(snapshot.model_auto_review.ready ? 'admin.accounts.schedulingStatus.autoReady' : 'admin.accounts.schedulingStatus.autoObserving') }}</p>
        </template>
        <p v-if="snapshot.scheduling_mode === 'quality_preferred'" class="text-xs text-gray-500">{{ t('admin.accounts.schedulingStatus.qualityPolicy') }}</p>
        <p class="text-sm">{{ t('admin.accounts.schedulingStatus.current') }}: {{ snapshot.candidates.find(candidate => candidate.current)?.name || (snapshot.current_account_id ? `#${snapshot.current_account_id}` : t('admin.accounts.schedulingStatus.unbound')) }}</p>
        <p v-if="snapshot.transition" class="break-words text-xs text-gray-500">{{ t(`admin.accounts.schedulingStatus.${snapshot.transition.reason}`) }} · {{ formatDateTime(snapshot.transition.changed_at) }} · #{{ snapshot.transition.previous_account_id }} → #{{ snapshot.transition.account_id }}</p>
        <div class="max-h-96 overflow-auto">
          <table class="w-full min-w-[720px] text-left text-sm">
            <thead><tr><th class="p-2">{{ t('admin.accounts.refreshScheduling.account') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.order') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.evidence') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.successRate') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.recentFailures') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.lastFailure') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.cooldown') }}</th></tr></thead>
            <tbody><tr v-for="candidate in snapshot.candidates" :key="candidate.account_id" class="border-t border-gray-100 dark:border-dark-700">
              <td class="min-w-32 break-words p-2">{{ candidate.name }} (#{{ candidate.account_id }})<p v-if="candidate.capability_source" class="text-xs text-gray-500">{{ t(`admin.accounts.schedulingStatus.capabilitySources.${candidate.capability_source}`) }}<span v-if="candidate.upstream_model"> · {{ candidate.upstream_model }}</span></p><p v-if="candidate.account_rate_limit != null" class="text-xs text-gray-500">{{ t('admin.groups.accountRateLimit.status', { rate: candidate.account_rate_multiplier ?? '?', limit: candidate.account_rate_limit }) }}</p><p v-if="candidate.reason" class="text-xs text-gray-500">{{ t(`admin.accounts.schedulingStatus.${candidate.reason}`) }}</p></td>
              <td class="whitespace-nowrap p-2">{{ candidate.eligible ? candidate.rank : '-' }}<p v-if="candidate.current" class="text-xs text-blue-600">{{ t('admin.accounts.schedulingStatus.current') }}</p><p v-if="['quality_preferred', 'model_auto', 'smart_channel'].includes(snapshot.scheduling_mode || '') && candidate.account_id === snapshot.recommended_account_id" class="text-xs text-green-600">{{ t('admin.accounts.schedulingStatus.recommended') }}</p></td>
              <td class="whitespace-nowrap p-2">{{ t(`admin.accounts.schedulingStatus.${candidate.confidence}`) }}<p class="text-xs text-gray-500">{{ candidate.samples }} / {{ snapshot.minimum_samples }}</p><p v-if="candidate.intelligence_verdict" :title="candidate.intelligence_reason" class="mt-1 text-xs" :class="candidate.intelligence_verdict === 'degraded' ? 'text-red-600' : 'text-gray-500'">{{ t('pelicanTests.title') }}: {{ t(`pelicanTests.assessment.${candidate.intelligence_verdict}`) }}<span v-if="candidate.intelligence_at" class="block">{{ formatDateTime(candidate.intelligence_at) }}</span></p></td>
              <td class="whitespace-nowrap p-2">{{ candidate.success_rate === null ? '-' : `${(candidate.success_rate * 100).toFixed(1)}%` }}<p v-if="candidate.quality" class="text-xs text-gray-500">{{ t('admin.accounts.schedulingStatus.weekRate') }}: {{ rate(candidate.quality.successes_7d, candidate.quality.failures_7d) }}<br>{{ t('admin.accounts.schedulingStatus.hourRate') }}: {{ rate(candidate.quality.successes_1h, candidate.quality.failures_1h) }}</p></td>
              <td class="p-2">{{ candidate.recent_failures }}<p v-if="candidate.degradation?.status === 'pending'" class="mt-1 text-xs text-amber-600">{{ t('admin.accounts.schedulingStatus.quality_pending') }}<span v-if="candidate.degradation.due_at" class="block">{{ t('admin.accounts.schedulingStatus.confirmationDue', { time: formatDateTime(candidate.degradation.due_at) }) }}</span></p><p v-else-if="candidate.degradation?.status === 'confirmed'" class="mt-1 text-xs text-red-600">{{ t('admin.accounts.schedulingStatus.quality_confirmed') }}</p></td>
              <td class="whitespace-nowrap p-2">{{ candidate.last_failure_at ? formatDateTime(candidate.last_failure_at) : '-' }}</td>
              <td class="whitespace-nowrap p-2">{{ candidate.cooldown_seconds ? `${candidate.cooldown_seconds}s` : '-' }}</td>
            </tr></tbody>
          </table>
        </div>
        <div v-if="['quality_preferred', 'model_auto', 'smart_channel'].includes(snapshot.scheduling_mode || '')" class="max-h-96 overflow-auto">
          <table class="w-full min-w-[760px] text-left text-sm">
            <thead><tr><th class="p-2">{{ t('admin.accounts.refreshScheduling.account') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.qualityEvidence') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.qualityHistory') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.speed') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.lastTest') }}</th></tr></thead>
            <tbody><tr v-for="candidate in snapshot.candidates" :key="candidate.account_id" class="border-t border-gray-100 dark:border-dark-700">
              <td class="p-2">{{ candidate.name }} (#{{ candidate.account_id }})<p v-if="candidate.scheduling_preferred" class="text-xs text-blue-600">{{ t('admin.accounts.schedulingPreferred') }} · {{ candidate.priority }}</p></td>
              <td class="p-2">{{ t(`admin.accounts.schedulingStatus.quality_${candidate.degradation?.status === 'pending' ? 'pending' : candidate.degradation?.status === 'confirmed' ? 'confirmed' : candidate.quality?.tier || 'unknown'}`) }}<p class="text-xs text-gray-500">{{ t('admin.accounts.schedulingStatus.streak', { count: candidate.quality?.normal_streak || 0 }) }}</p><p v-if="candidate.quality?.test_effort" class="text-xs text-gray-500">{{ t('admin.accounts.schedulingStatus.testEffort', { effort: candidate.quality.test_effort }) }}</p></td>
              <td class="p-2"><template v-if="candidate.quality">{{ t('admin.accounts.schedulingStatus.historyCounts', candidate.quality) }}<p class="text-xs text-gray-500">{{ t('admin.accounts.schedulingStatus.answerCounts', candidate.quality) }}</p></template><template v-else>—</template></td>
              <td class="whitespace-nowrap p-2">P50 {{ milliseconds(candidate.quality?.latency_p50_ms) }} · P95 {{ milliseconds(candidate.quality?.latency_p95_ms) }}<p class="text-xs text-gray-500">{{ candidate.quality?.effort || '—' }} · {{ t('admin.accounts.schedulingStatus.speedSamples', { count: candidate.quality?.latency_samples || 0 }) }}</p></td>
              <td class="whitespace-nowrap p-2">{{ candidate.quality?.last_test_at ? formatDateTime(candidate.quality.last_test_at) : '—' }}</td>
            </tr></tbody>
          </table>
        </div>
        <div v-if="['model_auto', 'smart_channel'].includes(snapshot.scheduling_mode || '')" class="max-h-96 overflow-auto">
          <table class="w-full min-w-[800px] text-left text-sm">
            <thead><tr><th class="p-2">{{ t('admin.accounts.refreshScheduling.account') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.autoDecision') }}</th><th class="p-2">{{ t('admin.accounts.schedulingStatus.autoBuckets') }}</th></tr></thead>
            <tbody><tr v-for="candidate in snapshot.candidates" :key="candidate.account_id" class="border-t border-gray-100 dark:border-dark-700">
              <td class="p-2">{{ candidate.name }} (#{{ candidate.account_id }})</td>
              <td class="p-2">{{ t(`admin.accounts.schedulingStatus.autoReasons.${candidate.model_auto_reason || 'insufficient_samples'}`) }}</td>
              <td class="p-2"><template v-if="candidate.model_auto?.buckets.length"><p v-for="bucket in candidate.model_auto.buckets" :key="bucket.context_bucket" class="whitespace-nowrap text-xs">{{ candidate.model_auto.effort || '—' }} · {{ contextBuckets[bucket.context_bucket] }} · {{ bucket.samples }} {{ t('admin.accounts.schedulingStatus.autoSamples') }} · P50 {{ milliseconds(bucket.p50_ms) }} / P95 {{ milliseconds(bucket.p95_ms) }} · {{ bucket.output_tps.toFixed(1) }} tok/s · {{ t('admin.accounts.schedulingStatus.autoCost', { value: bucket.cost_ratio.toFixed(3) }) }}</p></template><span v-else>—</span></td>
            </tr></tbody>
          </table>
        </div>
      </div>
      <div v-if="results.length" class="max-h-80 overflow-auto">
        <table class="w-full text-left text-sm">
          <thead><tr><th class="p-2">{{ t('admin.accounts.refreshScheduling.account') }}</th><th class="p-2">{{ t('admin.accounts.refreshScheduling.result') }}</th><th class="p-2">{{ t('admin.accounts.refreshScheduling.latency') }}</th></tr></thead>
          <tbody><tr v-for="result in results" :key="result.account_id" class="border-t border-gray-100 dark:border-dark-700">
            <td class="p-2">{{ result.name }} (#{{ result.account_id }})</td>
            <td class="p-2"><span>{{ t(`admin.accounts.refreshScheduling.${result.status}`) }}</span><p v-if="result.error" class="max-w-md break-words text-xs text-gray-500">{{ result.error }}</p></td>
            <td class="whitespace-nowrap p-2">{{ result.first_output_ms ? `${Math.round(result.first_output_ms)} ms` : '—' }}</td>
          </tr></tbody>
        </table>
      </div>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="close">{{ t(running ? 'admin.accounts.refreshScheduling.cancel' : 'common.close') }}</button>
      <button v-if="readOnly" type="button" class="btn btn-primary" :disabled="loadingStatus || !model.trim()" @click="loadStatus">{{ t(loadingStatus ? 'common.loading' : 'common.refresh') }}</button>
      <button v-else type="button" class="btn btn-primary" :disabled="running || !model.trim() || !!winner" @click="start">{{ t('admin.accounts.refreshScheduling.start') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getModelAllowlistCandidates, getGroupSchedulingStatus, type GroupSchedulingStatus } from '@/api/admin/groups'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'
import type { AdminGroup } from '@/types'

interface ProbeEvent {
  type: string
  account_id?: number
  name?: string
  status?: string
  error?: string
  first_output_ms?: number
  total?: number
  model?: string
}
const props = defineProps<{ group: AdminGroup; readOnly?: boolean }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t } = useI18n()
const model = ref('')
const effort = ref('')
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
const contextBuckets = ['<8K', '8K–32K', '32K–128K', '≥128K']
const rate = (successes: number, failures: number) => successes + failures > 0 ? `${(100 * successes / (successes + failures)).toFixed(1)}% (${successes + failures})` : '—'
const milliseconds = (value?: number) => value && value > 0 ? `${(value / 1000).toFixed(1)}s` : '—'
const models = ref<string[]>([])
const running = ref(false)
const error = ref('')
const results = ref<ProbeEvent[]>([])
const total = ref(0)
const currentName = ref('')
const winner = ref<ProbeEvent | null>(null)
const snapshot = ref<GroupSchedulingStatus | null>(null)
const loadingStatus = ref(false)
let closed = false
watch([model, effort], () => { snapshot.value = null })
let controller: AbortController | undefined
onMounted(async () => {
  try {
    models.value = (await getModelAllowlistCandidates(props.group.id)).filter(value => !/[?*]/.test(value))
    if (props.readOnly && !closed) { model.value = models.value[0] || ''; if (model.value) await loadStatus() }
  }
  catch { /* A concrete model can still be entered manually. */ }
})
onBeforeUnmount(() => { closed = true; controller?.abort() })
function close() { closed = true; controller?.abort(); emit('close') }

async function loadStatus() {
  if (loadingStatus.value || !model.value.trim()) return
  const requestedModel = model.value.trim()
  const requestedEffort = effort.value
  loadingStatus.value = true
  error.value = ''
  try {
    const data = requestedEffort ? await getGroupSchedulingStatus(props.group.id, requestedModel, requestedEffort) : await getGroupSchedulingStatus(props.group.id, requestedModel)
    if (!closed && model.value.trim() === requestedModel && effort.value === requestedEffort) snapshot.value = data
  } catch (cause) {
    if (!closed && model.value.trim() === requestedModel) error.value = extractApiErrorMessage(cause, t('common.unknownError'))
  } finally { loadingStatus.value = false }
}

async function start() {
  if (props.readOnly || running.value || !model.value.trim() || winner.value) return
  running.value = true
  error.value = ''
  results.value = []
  total.value = 0
  currentName.value = ''
  controller = new AbortController()
  let terminal = false
  try {
    const response = await fetch(buildApiUrl(`/admin/groups/${props.group.id}/refresh-scheduling`), {
      method: 'POST',
      headers: { Authorization: `Bearer ${localStorage.getItem('auth_token')}`, 'Content-Type': 'application/json', [ADMIN_UI_REQUEST_HEADER]: '1' },
      body: JSON.stringify({ model: model.value.trim() }),
      signal: controller.signal
    })
    if (!response.ok) {
      const detail = await response.json().catch(() => null)
      throw new Error(detail?.message || `HTTP ${response.status}`)
    }
    const reader = response.body?.getReader()
    if (!reader) throw new Error(t('admin.accounts.refreshScheduling.interrupted'))
    const decoder = new TextDecoder()
    let buffer = ''
    try {
      while (true) {
        const { done, value } = await reader.read()
        buffer += done ? decoder.decode() : decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          const event: ProbeEvent = JSON.parse(line.slice(6))
          if (event.type === 'start') total.value = event.total || 0
          else if (event.type === 'testing') currentName.value = event.name || ''
          else if (event.type === 'result') results.value.push(event)
          else if (event.type === 'error') { terminal = true; error.value = event.error || t('common.unknownError') }
          else if (event.type === 'complete') { terminal = true; winner.value = event; emit('updated') }
        }
        if (done) break
      }
    } finally { reader.releaseLock() }
    if (!terminal) throw new Error(t('admin.accounts.refreshScheduling.interrupted'))
  } catch (cause) {
    if (!controller.signal.aborted) error.value = cause instanceof Error ? cause.message : t('common.unknownError')
  } finally { running.value = false }
}
</script>
