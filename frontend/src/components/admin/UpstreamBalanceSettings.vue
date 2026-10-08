<template>
  <section class="border-t border-gray-200 px-6 py-5 dark:border-dark-700">
    <h2 class="mb-4 text-lg font-semibold">{{ t('admin.accounts.balance.title') }}</h2>
    <p v-if="loading" role="status">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="mb-3 text-sm text-red-600">{{ error }}</p>
    <button v-if="!loaded && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('admin.accounts.balance.retry') }}</button>
    <form v-if="loaded" class="space-y-4" @submit.prevent="save">
      <fieldset :disabled="saving" class="space-y-4">
        <label class="flex items-center gap-2"><input v-model="settings.enabled" type="checkbox" @change="saved = false" />{{ t('admin.accounts.balance.globalEnabled') }}</label>
        <div class="grid max-w-2xl gap-4 sm:grid-cols-2">
          <label class="block text-sm">{{ t('admin.accounts.balance.interval') }}<input v-model.number="settings.interval_minutes" class="input mt-1 w-full" type="number" min="5" max="1440" step="1" required @input="saved = false" /></label>
          <label class="block text-sm">{{ t('admin.accounts.balance.threshold') }}<input v-model.number="settings.low_balance_threshold" class="input mt-1 w-full" type="number" min="0" max="1000000000" step="any" required @input="saved = false" /></label>
        </div>
        <button type="submit" class="btn btn-primary">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
      </fieldset>
      <p v-if="saved" role="status" class="text-sm text-green-600">{{ t('admin.accounts.balance.saved') }}</p>
    </form>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountsAPI } from '@/api/admin/accounts'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { UpstreamBalanceSettings } from '@/types'
const { t } = useI18n()
const settings = ref<UpstreamBalanceSettings>({ enabled: true, interval_minutes: 30, low_balance_threshold: 5 })
const loading = ref(false), loaded = ref(false), saving = ref(false), saved = ref(false), error = ref('')
async function load() {
  loading.value = true
  error.value = ''
  try { settings.value = await accountsAPI.getBalanceSettings(); loaded.value = true }
  catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.accounts.balance.requestFailed')) }
  finally { loading.value = false }
}
async function save() {
  saving.value = true
  saved.value = false
  error.value = ''
  try { settings.value = await accountsAPI.updateBalanceSettings(settings.value); saved.value = true }
  catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.accounts.balance.requestFailed')) }
  finally { saving.value = false }
}
onMounted(load)
</script>
