<template>
  <div class="mt-6 border-t border-gray-200 pt-5 dark:border-dark-700">
    <h3 class="mb-4 text-base font-semibold">{{ t(`${key}.title`) }}</h3>
    <p v-if="loading" role="status" class="text-sm">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="mb-3 break-words text-sm text-red-600">{{ error }}</p>
    <button v-if="!settings && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('admin.accounts.balance.retry') }}</button>
    <form v-if="settings" class="max-w-3xl space-y-4" @submit.prevent="save" @input="dirty = true; saved = false" @change="dirty = true; saved = false">
      <fieldset :disabled="busy" class="space-y-4">
        <div class="flex flex-wrap gap-x-6 gap-y-3 text-sm">
          <label class="flex items-center gap-2"><input v-model="settings.enabled" type="checkbox" />{{ t(`${key}.enabled`) }}</label>
          <label class="flex items-center gap-2"><input v-model="settings.notify_recovery" type="checkbox" />{{ t(`${key}.recovery`) }}</label>
        </div>
        <p v-if="!settings.encryption_key_configured" role="status" class="text-sm text-amber-600">{{ t(`${key}.encryptionRequired`) }}</p>
        <fieldset v-for="(channel, index) in settings.channels" :key="channel.id" class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <div class="flex flex-wrap items-center gap-3">
            <label class="flex items-center gap-2 text-sm"><input v-model="channel.enabled" type="checkbox" />{{ t('common.enabled') }}</label>
            <span class="min-w-0 flex-1 break-words text-sm font-medium">{{ channel.name || t(`${key}.providers.${channel.provider}`) }}</span>
            <button type="button" class="flex h-8 w-8 items-center justify-center rounded hover:bg-gray-100 dark:hover:bg-dark-700" :disabled="dirty || !savedIds.has(channel.id) || busy" :title="t(`${key}.test`)" :aria-label="t(`${key}.test`)" @click="test(channel.id)"><Icon name="play" size="sm" /></button>
            <button type="button" class="flex h-8 w-8 items-center justify-center rounded text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20" :title="t('common.delete')" :aria-label="t('common.delete')" @click="remove(index)"><Icon name="trash" size="sm" /></button>
          </div>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block text-sm">{{ t(`${key}.name`) }}<input v-model="channel.name" class="input mt-1 w-full" maxlength="100" /></label>
            <label class="block text-sm">{{ t(`${key}.provider`) }}<select v-model="channel.provider" class="input mt-1 w-full" @change="changeProvider(channel)"><option v-for="provider in providers" :key="provider" :value="provider">{{ t(`${key}.providers.${provider}`) }}</option></select></label>
          </div>
          <label v-if="channel.provider === 'email'" class="block text-sm">{{ t(`${key}.recipient`) }}<input v-model="channel.email" class="input mt-1 w-full" type="email" maxlength="254" required /></label>
          <template v-else>
            <label class="block text-sm">{{ t(`${key}.url`) }}<input v-model="channel.url" class="input mt-1 w-full" type="password" autocomplete="new-password" spellcheck="false" maxlength="4096" :required="!channel.url_configured" :placeholder="channel.url_configured ? t(`${key}.retain`) : 'https://'" /></label>
            <label v-if="channel.provider !== 'wecom'" class="block text-sm">{{ t(`${key}.secret`) }}<input v-model="channel.secret" class="input mt-1 w-full" type="password" autocomplete="new-password" maxlength="8192" :placeholder="channel.secret_configured ? t(`${key}.retain`) : ''" /></label>
            <label v-if="channel.secret_configured" class="flex items-center gap-2 text-sm"><input v-model="channel.clear_secret" type="checkbox" />{{ t(`${key}.clearSecret`) }}</label>
          </template>
        </fieldset>
        <div class="flex flex-wrap gap-3">
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="settings.channels.length >= 6" @click="add"><Icon name="plus" size="sm" />{{ t(`${key}.add`) }}</button>
          <button type="submit" class="btn btn-primary">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
        </div>
      </fieldset>
      <p v-if="saved" role="status" class="text-sm text-green-600">{{ t('admin.accounts.balance.saved') }}</p>
      <p v-if="testSent" role="status" class="text-sm text-green-600">{{ t(`${key}.testSent`) }}</p>
    </form>
    <div class="mt-6">
      <div class="mb-3 flex items-center gap-2">
        <h4 class="text-sm font-semibold">{{ t(`${key}.history`) }}</h4>
        <button type="button" class="flex h-8 w-8 items-center justify-center rounded hover:bg-gray-100 dark:hover:bg-dark-700" :title="t('admin.accounts.balance.refresh')" :aria-label="t('admin.accounts.balance.refresh')" :disabled="historyLoading" @click="loadHistory"><Icon name="refresh" size="sm" :class="{ 'animate-spin': historyLoading }" /></button>
      </div>
      <p v-if="historyError" role="alert" class="text-sm text-red-600">{{ historyError }}</p>
      <p v-else-if="!history.length" class="text-sm text-gray-500">{{ t(`${key}.empty`) }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full min-w-[600px] text-left text-xs">
          <thead class="border-b border-gray-200 text-gray-500 dark:border-dark-700"><tr><th class="py-2 pr-3">{{ t(`${key}.account`) }}</th><th class="py-2 pr-3">{{ t(`${key}.event`) }}</th><th class="py-2 pr-3">{{ t(`${key}.amount`) }}</th><th class="py-2 pr-3">{{ t(`${key}.delivery`) }}</th><th class="py-2">{{ t(`${key}.time`) }}</th></tr></thead>
          <tbody><tr v-for="event in history" :key="event.id" class="border-b border-gray-100 align-top dark:border-dark-700">
            <td class="max-w-[200px] break-words py-3 pr-3">{{ event.account_name }} <span class="text-gray-400">#{{ event.account_id }}</span></td>
            <td class="py-3 pr-3"><span :class="event.phase === 'low' ? 'text-amber-600' : 'text-green-600'">{{ t(`${key}.phases.${event.phase}`) }}</span><div class="mt-1 text-gray-500">{{ t(`admin.accounts.balance.scopes.${event.scope}`) }}</div></td>
            <td class="whitespace-nowrap py-3 pr-3">{{ event.remaining.toLocaleString(undefined, { maximumFractionDigits: 4 }) }} {{ event.currency }}</td>
            <td class="max-w-[220px] break-words py-3 pr-3"><div>{{ t(`${key}.statuses.${event.status}`) }}</div><div v-for="(delivery, id) in event.deliveries" :key="id" class="mt-1" :class="delivery.status === 'failed' ? 'text-red-600' : 'text-gray-500'">{{ delivery.name || t(`${key}.providers.${delivery.provider}`) }}: {{ t(`${key}.statuses.${delivery.status}`) }}</div></td>
            <td class="whitespace-nowrap py-3">{{ new Date(event.observed_at).toLocaleString() }}</td>
          </tr></tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getBalanceNotificationHistory, getBalanceNotificationSettings, saveBalanceNotificationSettings, testBalanceNotification } from '@/api/admin/accounts'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { BalanceNotificationChannel, BalanceNotificationEvent, BalanceNotificationSettings as NotificationSettings } from '@/types'

const { t } = useI18n()
const key = 'admin.accounts.balance.notifications'
const providers = ['email', 'wecom', 'dingtalk', 'feishu', 'webhook'] as const
const settings = ref<NotificationSettings | null>(null)
const history = ref<BalanceNotificationEvent[]>([])
const savedIds = ref(new Set<string>())
const loading = ref(false), saving = ref(false), testing = ref(false), saved = ref(false), dirty = ref(false), testSent = ref(false), historyLoading = ref(false)
const error = ref(''), historyError = ref('')
const busy = computed(() => saving.value || testing.value)

async function load() {
  loading.value = true
  error.value = ''
  try { settings.value = await getBalanceNotificationSettings(); savedIds.value = new Set(settings.value.channels.map(c => c.id)); dirty.value = false }
  catch (cause) { error.value = extractApiErrorMessage(cause, t(`${key}.failed`)) }
  finally { loading.value = false }
}
async function loadHistory() {
  historyLoading.value = true
  historyError.value = ''
  try { history.value = await getBalanceNotificationHistory() }
  catch (cause) { historyError.value = extractApiErrorMessage(cause, t(`${key}.failed`)) }
  finally { historyLoading.value = false }
}
function add() {
  settings.value?.channels.push({ id: crypto.randomUUID(), name: '', provider: 'email', enabled: true, email: '', url_configured: false, secret_configured: false })
  dirty.value = true
  saved.value = false
}
function remove(index: number) { settings.value?.channels.splice(index, 1); dirty.value = true; saved.value = false }
function changeProvider(channel: BalanceNotificationChannel) { channel.url = ''; channel.secret = ''; channel.url_configured = false; channel.secret_configured = false; channel.clear_secret = false; channel.email = '' }
async function save() {
  if (!settings.value || busy.value) return
  saving.value = true
  saved.value = false
  testSent.value = false
  error.value = ''
  try {
    settings.value = await saveBalanceNotificationSettings(settings.value)
    savedIds.value = new Set(settings.value.channels.map(c => c.id))
    dirty.value = false
    saved.value = true
  } catch (cause) { error.value = extractApiErrorMessage(cause, t(`${key}.failed`)) }
  finally { saving.value = false }
}
async function test(id: string) {
  if (busy.value || dirty.value) return
  testing.value = true
  error.value = ''
  testSent.value = false
  try { await testBalanceNotification(id); testSent.value = true }
  catch (cause) { error.value = extractApiErrorMessage(cause, t(`${key}.failed`)) }
  finally { testing.value = false }
}
onMounted(() => { void load(); void loadHistory() })
</script>
