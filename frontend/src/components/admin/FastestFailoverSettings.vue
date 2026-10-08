<template>
  <section class="card" aria-labelledby="fastest-failover-title">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 id="fastest-failover-title" class="text-lg font-semibold">{{ t('admin.settings.fastestFailover.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('admin.settings.fastestFailover.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <button v-if="!loaded && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('admin.settings.directAccess.retry') }}</button>
      <fieldset v-if="loaded" :disabled="saving" class="space-y-4">
        <div class="grid gap-4 md:grid-cols-3">
          <label v-for="field in fields" :key="field.key" class="block text-sm">
            {{ t(`admin.settings.fastestFailover.${field.key}`) }}
            <input v-model.number="settings[field.key]" :data-testid="field.key" class="input mt-2 w-full" type="number" min="1" :max="field.max" step="1" required @keydown.enter.prevent="save" />
            <span class="mt-1 block text-xs text-gray-500">{{ t(field.key === 'minimum_samples' ? 'admin.settings.fastestFailover.sampleRange' : 'admin.settings.fastestFailover.range', { max: field.max }) }}</span>
          </label>
        </div>
        <p class="text-xs text-gray-500">{{ t('admin.settings.fastestFailover.scope') }}</p>
        <button type="button" class="btn btn-primary" @click="save">{{ t(saving ? 'common.saving' : 'admin.settings.fastestFailover.save') }}</button>
      </fieldset>
      <p v-if="saved" role="status" class="text-sm text-green-600">{{ t('admin.settings.fastestFailover.saved') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getFastestFailoverSettings, updateFastestFailoverSettings, type FastestFailoverSettings } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const fields: { key: keyof FastestFailoverSettings; max: number }[] = [
  { key: 'first_output_timeout_seconds', max: 600 },
  { key: 'stream_idle_timeout_seconds', max: 1800 },
  { key: 'model_cooldown_seconds', max: 86400 },
  { key: 'total_attempt_budget_seconds', max: 3600 },
  { key: 'minimum_samples', max: 10000 }
]
const defaults: FastestFailoverSettings = { first_output_timeout_seconds: 60, stream_idle_timeout_seconds: 120, model_cooldown_seconds: 120, total_attempt_budget_seconds: 120, minimum_samples: 10 }
const settings = ref<FastestFailoverSettings>({ ...defaults })
const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const saved = ref(false)
const error = ref('')
watch(settings, () => { saved.value = false }, { deep: true, flush: 'sync' })
async function load() {
  loading.value = true
  error.value = ''
  try { settings.value = { ...defaults, ...await getFastestFailoverSettings() }; loaded.value = true }
  catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.settings.fastestFailover.loadFailed')) }
  finally { loading.value = false }
}
async function save() {
  if (!loaded.value || saving.value) return
  error.value = ''
  saved.value = false
  if (fields.some(({ key, max }) => !Number.isInteger(settings.value[key]) || settings.value[key] < 1 || settings.value[key] > max)) {
    error.value = t('admin.settings.fastestFailover.invalid')
    return
  }
  saving.value = true
  try { settings.value = await updateFastestFailoverSettings(settings.value); saved.value = true }
  catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.settings.fastestFailover.saveFailed')) }
  finally { saving.value = false }
}
onMounted(load)
</script>
