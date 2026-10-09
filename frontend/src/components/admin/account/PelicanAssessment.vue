<template>
  <div v-if="html" class="shrink-0 border-t border-gray-200 px-3 py-2 dark:border-dark-600" data-testid="pelican-assessment" :aria-busy="busy || record?.status === 'running'">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div class="flex min-w-0 flex-wrap items-center gap-2 text-xs">
        <span class="text-gray-500">{{ t('pelicanTests.assessment.provider') }}</span>
        <span v-if="record?.status === 'succeeded' && record.assessment" class="font-medium" :class="qualityClass" data-testid="assessment-quality">{{ t(`pelicanTests.assessment.${record.assessment.quality}`) }}</span>
        <span v-else-if="record?.status === 'running'" role="status" class="text-primary-600 dark:text-primary-300">{{ t('pelicanTests.assessment.running') }}</span>
        <span v-else-if="error || record?.status === 'failed'" class="text-gray-500">{{ t('pelicanTests.assessment.unknown') }}</span>
        <span v-else-if="!loading" class="text-gray-400">{{ t('pelicanTests.assessment.notEvaluated') }}</span>
        <span v-if="record?.assessment?.checked_at" class="text-gray-400">{{ formatDateTimeToMinute(record.assessment.checked_at) }}</span>
      </div>
      <button v-if="record?.status !== 'succeeded'" type="button" class="btn btn-secondary inline-flex min-h-8 shrink-0 items-center gap-1.5 text-xs" :disabled="loading || busy || retrying || record?.status === 'running' || tooLarge" :title="t('pelicanTests.assessment.submitTitle')" data-testid="assess-pelican" @click="start">
        <Icon :name="busy || record?.status === 'running' ? 'refresh' : 'brain'" size="sm" :class="{ 'animate-spin': busy || record?.status === 'running' }" />
        {{ t(error || record?.status === 'failed' ? 'pelicanTests.assessment.retry' : 'pelicanTests.assessment.evaluate') }}
      </button>
      <button v-else type="button" class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700" :aria-label="t('pelicanTests.assessment.details')" :title="t('pelicanTests.assessment.details')" @click="details = !details"><Icon name="infoCircle" size="sm" /></button>
    </div>
    <p v-if="tooLarge" class="mt-1 break-words text-xs text-amber-600">{{ t('pelicanTests.assessment.tooLarge') }}</p>
    <p v-if="error || record?.status === 'failed'" role="alert" class="mt-1 break-words text-xs text-gray-500">{{ error || t('pelicanTests.assessment.failed') }}</p>
    <div v-if="details && record?.assessment" class="mt-2 space-y-1 break-words text-xs text-gray-500" data-testid="assessment-details">
      <p>{{ record.assessment.reason }}</p>
      <p v-if="record.assessment.source">{{ t('pelicanTests.assessment.source') }}: {{ record.assessment.source }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { pelicanAssessmentsAPI, type PelicanAssessmentRecord } from '@/api/admin/pelicanAssessments'
import { extractPelicanSource } from '@/utils/pelicanHtml'
import { formatDateTimeToMinute } from '@/utils/format'

const props = defineProps<{ output: string }>()
const { t } = useI18n()
const html = computed(() => extractPelicanSource(props.output))
const tooLarge = computed(() => new TextEncoder().encode(html.value).length > 2 * 1024 * 1024)
const record = ref<PelicanAssessmentRecord | null>(null)
const loading = ref(false)
const busy = ref(false)
const retrying = ref(false)
const details = ref(false)
const error = ref('')
const qualityClass = computed(() => ({ normal: 'text-emerald-600 dark:text-emerald-400', degraded: 'text-red-600 dark:text-red-400', unknown: 'text-gray-500' }[record.value?.assessment?.quality || 'unknown']))
let controller = new AbortController()
let timer: ReturnType<typeof setTimeout> | undefined
let retryTimer: ReturnType<typeof setTimeout> | undefined
let revision = 0

function schedule() {
  clearTimeout(timer)
  if (record.value?.status !== 'running') return
  const delay = Math.min(3600, Math.max(2, record.value.retry_after_seconds || 3)) * 1000
  timer = setTimeout(poll, delay)
}

async function poll() {
  const current = record.value
  if (!current || controller.signal.aborted) return
  const version = revision
  try {
    const result = await pelicanAssessmentsAPI.poll(current.hash, controller.signal)
    if (version !== revision) return
    record.value = result
    error.value = ''
    schedule()
  } catch {
    if (version !== revision || controller.signal.aborted) return
    error.value = t('pelicanTests.assessment.failed')
    record.value = null
  }
}

async function start() {
  if (busy.value || loading.value || retrying.value || tooLarge.value || !html.value) return
  const version = revision
  busy.value = true
  error.value = ''
  try {
    const result = await pelicanAssessmentsAPI.start(html.value, controller.signal)
    if (version !== revision) return
    record.value = result
    schedule()
  } catch (failure) {
    if (version === revision && !controller.signal.aborted) {
      error.value = t('pelicanTests.assessment.failed')
      const delay = Number((failure as { metadata?: { retry_after_seconds?: string } })?.metadata?.retry_after_seconds)
      if (Number.isFinite(delay) && delay > 0) {
        retrying.value = true
        retryTimer = setTimeout(() => { retrying.value = false }, Math.min(delay, 3600) * 1000)
      }
    }
  } finally {
    if (version === revision) busy.value = false
  }
}

watch(html, async (value) => {
  revision++
  const version = revision
  controller.abort()
  controller = new AbortController()
  clearTimeout(timer)
  clearTimeout(retryTimer)
  record.value = null
  error.value = ''
  details.value = false
  busy.value = false
  retrying.value = false
  loading.value = false
  if (!value || tooLarge.value) return
  loading.value = true
  try {
    const saved = await pelicanAssessmentsAPI.lookup(value, controller.signal)
    if (version !== revision) return
    record.value = saved
    schedule()
  } catch {
    if (version === revision && !controller.signal.aborted) error.value = t('pelicanTests.assessment.loadFailed')
  } finally {
    if (version === revision) loading.value = false
  }
}, { immediate: true })

onBeforeUnmount(() => { revision++; controller.abort(); clearTimeout(timer); clearTimeout(retryTimer) })
</script>
