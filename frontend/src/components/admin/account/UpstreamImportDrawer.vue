<template>
  <BaseDialog :show="show" :title="tr('batchTitle')" width="wide" content-class="upstream-import-drawer" :show-close-button="!working" :close-on-escape="!working" @close="emit('close')">
    <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">{{ site.name }} · {{ tr('batchHint') }}</p>
    <div v-if="error" role="alert" class="mb-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</div>
    <fieldset :disabled="frozen || working" class="grid min-w-0 gap-4 sm:grid-cols-2">
      <label class="sm:col-span-2"><span class="input-label">{{ tr('group') }}</span><select v-model="groupId" class="input"><option :value="0">{{ tr('choose') }}</option><option v-for="group in compatibleGroups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label>
      <label><span class="input-label">{{ tr('platform') }}</span><select v-model="platform" class="input"><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option></select></label>
      <label><span class="input-label">{{ tr('prefix') }}</span><input v-model="prefix" maxlength="60" class="input" @input="updateNames" /></label>
      <label><span class="input-label">{{ tr('concurrency') }}</span><input v-model.number="concurrency" type="number" min="1" max="1000" class="input" /></label>
      <label><span class="input-label">{{ tr('rate') }}</span><input v-model.number="rate" type="number" min="0" max="100" step="0.01" class="input" /></label>
    </fieldset>
    <div class="my-5 flex flex-wrap items-center justify-between gap-2">
      <h4 class="font-semibold">{{ t('upstreamSites.selected', { count: rows.length }) }}</h4>
      <button v-if="!frozen" type="button" class="btn btn-secondary" :disabled="working" @click="loadModels">{{ working ? tr('loading') : tr('loadPreview') }}</button>
    </div>
    <div class="space-y-3">
      <section v-for="row in rows" :key="row.key.id" class="min-w-0 rounded-xl border border-gray-200 p-4 dark:border-dark-600" :data-import-key="row.key.id">
        <div class="mb-3 flex items-center justify-between gap-3">
          <div class="min-w-0"><h4 class="truncate text-sm font-medium">{{ row.key.name }}</h4><p class="text-xs text-gray-500">{{ row.key.group_name || row.key.group_id }} · #{{ row.key.id }}</p></div>
          <button v-if="!frozen" type="button" class="text-xs text-gray-500" :disabled="working" @click="rows = rows.filter(item => item !== row)">{{ tr('delete') }}</button>
        </div>
        <p v-if="duplicate(row.key.id)" class="text-sm text-gray-500">{{ tr('duplicate') }}</p>
        <template v-else>
          <label><span class="input-label">{{ tr('importName') }}</span><input v-model="row.name" class="input" maxlength="100" :disabled="frozen || working" /></label>
          <fieldset class="mt-3" :disabled="frozen || working"><legend class="mb-2 text-xs text-gray-500">{{ tr('selectModels') }}</legend>
            <div class="max-h-40 space-y-1 overflow-y-auto rounded-lg bg-gray-50 p-2 dark:bg-dark-800">
              <label v-for="model in row.models" :key="model" class="flex min-w-0 items-start gap-2 text-sm"><input v-model="row.selected" type="checkbox" :value="model" class="mt-1" /><span class="break-all">{{ model }}</span></label>
              <p v-if="!row.models.length" class="text-xs text-gray-500">{{ row.error || tr('noCatalog') }}</p>
            </div>
          </fieldset>
          <p v-if="draftFor(row.key.id)" role="status" class="mt-3 break-words text-sm" :class="draftFor(row.key.id)?.status === 'failed' ? 'text-red-600 dark:text-red-400' : 'text-primary-600 dark:text-primary-400'">
            {{ draftLabel(row.key.id) }}<span v-if="draftFor(row.key.id)?.accountId"> · #{{ draftFor(row.key.id)?.accountId }}</span><span v-if="draftFor(row.key.id)?.error">：{{ draftFor(row.key.id)?.error }}</span>
          </p>
        </template>
      </section>
    </div>
    <p class="mt-5 rounded-lg bg-primary-50 p-3 text-xs leading-6 text-primary-800 dark:bg-primary-950/30 dark:text-primary-300">{{ frozen ? tr('frozen') : tr('batchSafety') }}</p>
    <template #footer>
      <div class="flex w-full flex-wrap items-center justify-between gap-3">
        <p class="text-xs text-gray-500" role="status">{{ t('upstreamSites.completed', { success: successes, total: drafts.length || eligibleRows.length }) }}</p>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="running ? stopRequested = true : emit('close')">{{ running ? tr(stopRequested ? 'stopping' : 'stop') : tr('close') }}</button>
          <button type="button" class="btn btn-primary" :disabled="working || !eligibleRows.length || (frozen && drafts.every(draft => draft.status === 'success'))" @click="start">{{ frozen ? tr('retry') : tr('startImport') }}</button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { AdminGroup } from '@/types'
import { upstreamSitesAPI, runSiteImports, siteError, type ImportDraft, type SiteKey, type UpstreamSite, type SiteAccount } from '@/api/admin/upstreamSites'

const props = defineProps<{ show: boolean; site: UpstreamSite; keys: SiteKey[]; groups: AdminGroup[]; accounts: SiteAccount[] }>()
const emit = defineEmits<{ close: []; imported: []; models: [keyId: string, models: string[]] }>()
const { t } = useI18n()
const tr = (key: string) => t(`upstreamSites.${key}`)
const groupId = ref(0), platform = ref('openai'), prefix = ref(''), concurrency = ref(1), rate = ref(1)
const loading = ref(false), running = ref(false), frozen = ref(false), stopRequested = ref(false), error = ref('')
const drafts = ref<ImportDraft[]>([])
const skipped = ref<string[]>([])
const rows = ref<{ key: SiteKey; name: string; models: string[]; selected: string[]; error?: string }[]>([])
const compatibleGroups = computed(() => props.groups.filter(group => group.platform === platform.value || group.platform === 'composite'))
const working = computed(() => loading.value || running.value)
const duplicate = (keyId: string) => frozen.value ? skipped.value.includes(keyId) : props.accounts.some(account => account.key_id === keyId && account.platform === platform.value)
const eligibleRows = computed(() => rows.value.filter(row => !duplicate(row.key.id)))
const successes = computed(() => drafts.value.filter(draft => draft.status === 'success').length)
const draftFor = (keyId: string) => drafts.value.find(draft => draft.payload.key_id === keyId)
const draftLabel = (keyId: string) => tr(({ pending: 'ready', running: 'running', success: 'success', failed: 'failed' } as const)[draftFor(keyId)?.status || 'pending'])
let draftIdentity = ''
function nameFor(key: SiteKey) {
  let name = `${prefix.value}${key.name}`
  while (new TextEncoder().encode(name).length > 100) name = Array.from(name).slice(0, -1).join('')
  return name
}
function updateNames() { rows.value.forEach(row => { row.name = nameFor(row.key) }) }
watch(platform, () => { if (!frozen.value) groupId.value = 0 })
watch(() => props.show, show => {
  if (!show) return
  const identity = `${props.site.id}:${props.site.revision}:${props.keys.map(key => key.id).sort().join(',')}`
  if (draftIdentity === identity && frozen.value) return
  draftIdentity = identity
  prefix.value = `${props.site.name} · `; groupId.value = 0; error.value = ''; frozen.value = false; drafts.value = []
  rows.value = props.keys.map(key => {
    const models = props.site.catalogs?.[key.id]?.models || []
    return { key, name: nameFor(key), models: [...models], selected: models.slice(0, 1000) }
  })
})
async function loadModels() {
  loading.value = true; error.value = ''
  try {
    for (const row of eligibleRows.value) {
      row.error = undefined
      try {
        row.models = await upstreamSitesAPI.models(props.site.id, row.key.id)
        row.selected = row.models.slice(0, 1000)
        emit('models', row.key.id, row.models)
      } catch (cause) { row.error = siteError(cause); row.models = []; row.selected = [] }
    }
  } finally { loading.value = false }
}
async function start() {
  error.value = ''
  if (!frozen.value) {
    if (!groupId.value || !eligibleRows.value.length || eligibleRows.value.some(row => !row.selected.length || !row.name.trim() || new TextEncoder().encode(row.name).length > 100) || !Number.isInteger(concurrency.value) || concurrency.value < 1 || concurrency.value > 1000 || !Number.isFinite(rate.value) || rate.value < 0 || rate.value > 100) { error.value = tr('selectRequired'); return }
    skipped.value = rows.value.filter(row => duplicate(row.key.id)).map(row => row.key.id)
    drafts.value = eligibleRows.value.map(row => ({ idempotencyKey: crypto.randomUUID(), status: 'pending', payload: { key_id: row.key.id, revision: props.site.revision, name: row.name, platform: platform.value, group_id: groupId.value, concurrency: concurrency.value, rate_multiplier: rate.value, models: [...row.selected] } }))
    frozen.value = true
  }
  running.value = true; stopRequested.value = false
  try { await runSiteImports(props.site.id, drafts.value, () => stopRequested.value) }
  finally { running.value = false; emit('imported') }
}
</script>

<style>
.modal-overlay:has(.upstream-import-drawer) { justify-content: flex-end; padding: 0; }
.modal-content.upstream-import-drawer { width: min(640px, 100%); max-width: 640px; height: 100dvh; max-height: 100dvh; margin: 0; border-radius: 16px 0 0 16px; display: flex; flex-direction: column; }
.upstream-import-drawer .modal-body { flex: 1; min-height: 0; overflow-y: auto; }
.upstream-import-drawer .modal-footer { flex-shrink: 0; }
@media (max-width: 640px) { .modal-content.upstream-import-drawer { border-radius: 0; } }
</style>
