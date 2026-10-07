<template>
  <BaseDialog :show="true" :title="t('admin.accounts.refreshScheduling.title')" width="wide" @close="close">
    <div class="space-y-4">
      <p class="font-medium">{{ group.name }}</p>
      <p class="text-sm text-gray-500">{{ t('admin.accounts.refreshScheduling.description') }}</p>
      <p class="text-sm text-amber-600">{{ t('admin.accounts.refreshScheduling.cost') }}</p>
      <label class="block text-sm">
        {{ t('admin.accounts.refreshScheduling.model') }}
        <input v-model="model" list="scheduling-models" class="input mt-2 w-full" :disabled="running" maxlength="200" autocomplete="off" @keydown.enter.prevent="start" />
        <datalist id="scheduling-models"><option v-for="candidate in models" :key="candidate" :value="candidate" /></datalist>
      </label>
      <p class="text-xs text-gray-500">{{ t('admin.accounts.refreshScheduling.policy') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="running" role="status" class="text-sm">{{ t('admin.accounts.refreshScheduling.progress', { done: results.length, total, name: currentName }) }}</p>
      <p v-if="winner" role="status" class="rounded-lg bg-green-50 p-3 text-sm text-green-700 dark:bg-green-900/20">{{ t('admin.accounts.refreshScheduling.complete', { name: winner.name, id: winner.account_id, model: winner.model }) }}</p>
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
      <button type="button" class="btn btn-primary" :disabled="running || !model.trim() || !!winner" @click="start">{{ t('admin.accounts.refreshScheduling.start') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getModelAllowlistCandidates } from '@/api/admin/groups'
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
const props = defineProps<{ group: AdminGroup }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t } = useI18n()
const model = ref('')
const models = ref<string[]>([])
const running = ref(false)
const error = ref('')
const results = ref<ProbeEvent[]>([])
const total = ref(0)
const currentName = ref('')
const winner = ref<ProbeEvent | null>(null)
let controller: AbortController | undefined
onMounted(async () => {
  try { models.value = (await getModelAllowlistCandidates(props.group.id)).filter(value => !/[?*]/.test(value)) }
  catch { /* A concrete model can still be entered manually. */ }
})
onBeforeUnmount(() => controller?.abort())
function close() { controller?.abort(); emit('close') }

async function start() {
  if (running.value || !model.value.trim() || winner.value) return
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
