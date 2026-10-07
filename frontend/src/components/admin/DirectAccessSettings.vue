<template>
  <section class="card" aria-labelledby="direct-access-title">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 id="direct-access-title" class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.directAccess.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.directAccess.description') }}
      </p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <button v-if="!loaded && !loading" type="button" class="btn btn-secondary btn-sm" @click="load">
        {{ t('admin.settings.directAccess.retry') }}
      </button>
      <template v-if="loaded">
        <p v-if="hostname" class="break-all text-sm text-gray-700 dark:text-gray-300">
          {{ t('admin.settings.directAccess.endpoint') }}
          <code>https://{{ hostname }}/v1</code>
        </p>
        <p v-else class="text-sm text-amber-600 dark:text-amber-400">
          {{ t('admin.settings.directAccess.unavailable') }}
        </p>
        <fieldset :disabled="saving || !hostname" class="space-y-3">
          <div v-for="(entry, index) in entries" :key="index" class="grid items-end gap-3 md:grid-cols-[1fr_1fr_auto]">
            <label class="block text-sm text-gray-700 dark:text-gray-300">
              {{ t('admin.settings.directAccess.address') }}
              <input v-model="entry.cidr" data-testid="direct-access-address" class="input mt-1 w-full font-mono" type="text"
                placeholder="47.239.86.227 / 203.0.113.0/24" maxlength="64" autocomplete="off"
                @keydown.enter.prevent="save" />
            </label>
            <label class="block text-sm text-gray-700 dark:text-gray-300">
              {{ t('admin.settings.directAccess.note') }}
              <input v-model="entry.note" data-testid="direct-access-note" class="input mt-1 w-full" type="text"
                maxlength="100" @keydown.enter.prevent="save" />
            </label>
            <button type="button" class="btn btn-secondary" data-testid="direct-access-remove"
              :aria-label="t('admin.settings.directAccess.removeEntry', { index: index + 1 })" @click="entries.splice(index, 1); saved = false">
              {{ t('common.delete') }}
            </button>
          </div>
          <p v-if="entries.length === 0" class="text-sm text-amber-600 dark:text-amber-400">
            {{ t('admin.settings.directAccess.empty') }}
          </p>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.directAccess.hint') }}</p>
          <div class="flex flex-wrap gap-3">
            <button type="button" class="btn btn-secondary" data-testid="direct-access-add" :disabled="entries.length >= 256"
              @click="entries.push({ cidr: '', note: '' }); saved = false">
              {{ t('admin.settings.directAccess.add') }}
            </button>
            <button type="button" class="btn btn-primary" data-testid="direct-access-save" @click="save">
              {{ t(saving ? 'common.saving' : 'admin.settings.directAccess.save') }}
            </button>
          </div>
        </fieldset>
        <p v-if="saved" role="status" class="text-sm text-green-600 dark:text-green-400">
          {{ t('admin.settings.directAccess.saved') }}
        </p>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getDirectAccessSettings, updateDirectAccessSettings, type DirectAccessEntry } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const hostname = ref('')
const entries = ref<DirectAccessEntry[]>([])
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const saved = ref(false)
const error = ref('')

watch(entries, () => { saved.value = false }, { deep: true, flush: 'sync' })

async function load() {
  loading.value = true
  loaded.value = false
  error.value = ''
  try {
    const settings = await getDirectAccessSettings()
    hostname.value = settings.hostname
    entries.value = settings.entries.map(entry => ({ ...entry }))
    loaded.value = true
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.settings.directAccess.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!loaded.value || saving.value || !hostname.value) return
  error.value = ''
  saved.value = false
  if (entries.value.some(entry => !entry.cidr.trim())) {
    error.value = t('admin.settings.directAccess.addressRequired')
    return
  }
  saving.value = true
  try {
    const settings = await updateDirectAccessSettings(entries.value.map(entry => ({ cidr: entry.cidr.trim(), note: entry.note.trim() })))
    entries.value = settings.entries.map(entry => ({ ...entry }))
    saved.value = true
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.settings.directAccess.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
