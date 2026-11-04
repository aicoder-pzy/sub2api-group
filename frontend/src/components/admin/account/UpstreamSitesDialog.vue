<template>
  <BaseDialog :show="show" :title="t('zakoFeatures.sites.title')" width="wide" @close="emit('close')">
    <div class="space-y-6">
      <div class="flex flex-wrap gap-2">
        <select v-model="selectedSite" :disabled="busy" class="input min-w-0 flex-1" :aria-label="t('zakoFeatures.sites.select')" @change="selectSite">
          <option value="">{{ t('zakoFeatures.sites.newSite') }}</option>
          <option v-for="site in sites" :key="site.id" :value="site.id">{{ site.name }} · {{ site.kind }}</option>
        </select>
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="run(loadSites)">{{ t('zakoFeatures.sites.refresh') }}</button>
        <button v-if="selectedSite" type="button" class="btn btn-secondary" :disabled="busy" @click="deleteConfirm = true">{{ t('zakoFeatures.sites.delete') }}</button>
      </div>
      <form class="grid gap-3 sm:grid-cols-2" @submit.prevent="run(saveSite)">
        <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.name') }}</span><input v-model="form.name" required maxlength="100" class="input" /></label>
        <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.kind') }}</span><select v-model="form.kind" class="input"><option value="sub2api">Sub2API</option><option value="newapi">New API</option></select></label>
        <label class="space-y-1 sm:col-span-2"><span class="input-label">{{ t('zakoFeatures.sites.url') }}</span><input v-model="form.base_url" type="url" required class="input" placeholder="https://example.com" /></label>
        <label v-if="form.kind === 'newapi'" class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.userId') }}</span><input v-model.number="form.user_id" type="number" min="0" step="1" class="input" /></label>
        <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.proxy') }}</span><select v-model="form.proxy_id" class="input"><option :value="null">{{ t('zakoFeatures.sites.direct') }}</option><option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.name }}</option></select></label>
        <label class="space-y-1 sm:col-span-2"><span class="input-label">{{ t('zakoFeatures.sites.token') }}</span><input v-model="form.access_token" type="password" autocomplete="new-password" :required="!selectedSite" maxlength="16384" class="input" /><span class="block text-xs text-gray-500">{{ t('zakoFeatures.sites.tokenHint') }}</span></label>
        <div class="flex flex-wrap gap-2 sm:col-span-2"><button class="btn btn-primary" :disabled="busy">{{ t('zakoFeatures.sites.save') }}</button><button v-if="selectedSite" type="button" class="btn btn-secondary" :disabled="busy" @click="run(loadInventory)">{{ t('zakoFeatures.sites.keys') }}</button></div>
      </form>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-green-600">{{ notice }}</p>
      <p v-if="busy" role="status" class="text-sm text-gray-500">{{ t('zakoFeatures.sites.busy') }}</p>
      <template v-if="inventory">
        <form class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600" @submit.prevent="run(importKey)">
          <p class="text-xs text-gray-500">{{ t('zakoFeatures.sites.importHint') }}</p>
          <label class="block space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.chooseKey') }}</span><select v-model="keyId" required class="input" :disabled="busy" @change="resetModels"><option value="" disabled>{{ t('zakoFeatures.sites.chooseKey') }}</option><option v-for="key in inventory.keys" :key="key.id" :value="key.id" :disabled="!key.available">{{ key.name }} · {{ key.group_name || key.group_id }} · {{ t(key.available ? 'zakoFeatures.sites.active' : 'zakoFeatures.sites.inactive') }}</option></select></label>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.name') }}</span><input v-model="importName" required maxlength="100" class="input" /></label>
            <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.platform') }}</span><select v-model="platform" class="input"><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="gemini">Gemini</option></select></label>
            <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.localGroup') }}</span><select v-model="localGroup" required class="input"><option :value="0" disabled>{{ t('zakoFeatures.sites.localGroup') }}</option><option v-for="group in compatibleGroups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label>
            <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.concurrency') }}</span><input v-model.number="concurrency" type="number" required min="1" max="1000" step="1" class="input" /></label>
            <label class="space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.rate') }}</span><input v-model.number="rate" type="number" required min="0" max="100" step="0.0001" class="input" /></label>
          </div>
          <button type="button" class="btn btn-secondary" :disabled="busy || !keyId" @click="run(loadModels)">{{ t('zakoFeatures.sites.models') }}</button>
          <fieldset v-if="models.length" class="max-h-52 space-y-2 overflow-y-auto rounded-lg border border-gray-200 p-3 dark:border-dark-600"><legend class="text-sm">{{ t('zakoFeatures.sites.selectedModels', { count: selectedModels.length }) }}</legend><label v-for="model in models" :key="model" class="flex items-center gap-2 break-all text-sm"><input v-model="selectedModels" type="checkbox" :value="model" />{{ model }}</label></fieldset>
          <button class="btn btn-primary" :disabled="busy || !keyId || !localGroup || !selectedModels.length">{{ t('zakoFeatures.sites.import') }}</button>
        </form>
        <form class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" @submit.prevent="run(createKey)">
          <p class="text-xs text-gray-500">{{ t('zakoFeatures.sites.createHint') }}</p>
          <label class="block space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.keyName') }}</span><input v-model="keyName" required maxlength="100" class="input" /></label>
          <label class="block space-y-1"><span class="input-label">{{ t('zakoFeatures.sites.groups') }}</span><select v-model="upstreamGroup" required class="input"><option value="" disabled>{{ t('zakoFeatures.sites.groups') }}</option><option v-for="group in inventory.groups" :key="group.id" :value="group.id">{{ group.name }}{{ group.multiplier === undefined ? '' : ' · ' + group.multiplier + '×' }}</option></select></label>
          <label class="flex items-center gap-2 text-sm"><input v-model="confirmed" type="checkbox" required />{{ t('zakoFeatures.sites.confirmed') }}</label>
          <button class="btn btn-secondary" :disabled="busy || !confirmed">{{ t('zakoFeatures.sites.create') }}</button>
        </form>
      </template>
    </div>
  </BaseDialog>
  <ConfirmDialog :show="deleteConfirm" :title="t('zakoFeatures.sites.delete')" :message="t('zakoFeatures.sites.deleteConfirm')" @cancel="deleteConfirm = false" @confirm="run(deleteSite)" />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import type { AdminGroup, Proxy } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

interface Site { id: string; name: string; kind: string; base_url: string; user_id: number; proxy_id: number | null; revision: number }
interface Inventory { revision: number; keys: { id: string; name: string; group_id: string; group_name: string; available: boolean }[]; groups: { id: string; name: string; multiplier?: number }[] }
const props = defineProps<{ show: boolean; groups: Pick<AdminGroup, 'id' | 'name' | 'platform'>[]; proxies: Pick<Proxy, 'id' | 'name'>[] }>()
const emit = defineEmits<{ close: []; imported: [] }>()
const { t } = useI18n()
const endpoint = '/admin/accounts/upstream-sites'
const empty = () => ({ name: '', kind: 'sub2api', base_url: '', user_id: 0, proxy_id: null as number | null, access_token: '', revision: 0 })
const sites = ref<Site[]>([])
const selectedSite = ref('')
const form = ref(empty())
const inventory = ref<Inventory | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const deleteConfirm = ref(false)
const keyId = ref('')
const importName = ref('')
const platform = ref('openai')
const localGroup = ref(0)
const concurrency = ref(1)
const rate = ref(1)
const models = ref<string[]>([])
const selectedModels = ref<string[]>([])
const keyName = ref('Sub2API')
const upstreamGroup = ref('')
const confirmed = ref(false)
const compatibleGroups = computed(() => props.groups.filter(group => group.platform === platform.value || group.platform === 'composite'))
watch(platform, () => { localGroup.value = 0 })
watch(() => props.show, show => { if (show) void run(loadSites); else { form.value.access_token = ''; error.value = ''; notice.value = '' } })
async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await action() } catch (cause: unknown) { error.value = cause instanceof Error ? cause.message : t('zakoFeatures.sites.error') }
  finally { busy.value = false }
}
async function loadSites() { sites.value = (await apiClient.get<Site[]>(endpoint)).data }
function resetModels() {
  models.value = []; selectedModels.value = []
  const key = inventory.value?.keys.find(item => item.id === keyId.value)
  importName.value = key ? `${form.value.name} · ${key.name}`.slice(0, 100) : ''
}
function selectSite() {
  const site = sites.value.find(item => item.id === selectedSite.value)
  form.value = site ? { ...site, access_token: '' } : empty()
  inventory.value = null; keyId.value = ''; upstreamGroup.value = ''; confirmed.value = false; resetModels()
}
async function saveSite() {
  const response = selectedSite.value ? await apiClient.put<Site>(`${endpoint}/${selectedSite.value}`, form.value, { timeout: 30_000 }) : await apiClient.post<Site>(endpoint, form.value, { timeout: 30_000 })
  form.value.access_token = ''; selectedSite.value = response.data.id
  await loadSites(); selectSite(); notice.value = t('zakoFeatures.sites.saved')
}
async function loadInventory() {
  inventory.value = null; keyId.value = ''; resetModels()
  inventory.value = (await apiClient.get<Inventory>(`${endpoint}/${selectedSite.value}/inventory`, { timeout: 90_000 })).data
}
async function loadModels() {
  models.value = []; selectedModels.value = []
  models.value = (await apiClient.get<string[]>(`${endpoint}/${selectedSite.value}/keys/${encodeURIComponent(keyId.value)}/models`, { timeout: 90_000 })).data
}
async function importKey() {
  await apiClient.post(`${endpoint}/${selectedSite.value}/import`, { key_id: keyId.value, revision: inventory.value?.revision, name: importName.value, platform: platform.value, group_id: localGroup.value, concurrency: concurrency.value, rate_multiplier: rate.value, models: selectedModels.value }, { timeout: 120_000, headers: { 'Idempotency-Key': crypto.randomUUID() } })
  notice.value = t('zakoFeatures.sites.imported'); emit('imported')
}
async function createKey() {
  const requestedGroup = upstreamGroup.value
  confirmed.value = false
  await apiClient.post(`${endpoint}/${selectedSite.value}/keys`, { name: keyName.value, group_id: requestedGroup, revision: inventory.value?.revision, confirmed: true }, { timeout: 30_000 })
  await loadInventory(); notice.value = t('zakoFeatures.sites.created')
}
async function deleteSite() {
  deleteConfirm.value = false
  await apiClient.delete(`${endpoint}/${selectedSite.value}`)
  selectedSite.value = ''; selectSite(); await loadSites()
}
</script>
