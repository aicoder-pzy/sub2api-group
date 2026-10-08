<template>
  <div v-if="account.type === 'apikey'" class="flex min-w-[11rem] items-start gap-1">
    <div class="min-w-0 flex-1 text-xs">
      <div v-for="amount in snapshot?.amounts" :key="amount.scope" class="flex gap-2" :class="isLow(amount) ? 'text-amber-600 dark:text-amber-400' : ''">
        <span>{{ t(`admin.accounts.balance.scopes.${amount.scope}`) }}</span>
        <span class="font-mono">{{ amount.unlimited ? t('admin.accounts.balance.unlimited') : formatAmount(amount) }}</span>
      </div>
      <span v-if="!snapshot?.amounts?.length" class="text-gray-400">{{ t('admin.accounts.balance.unknown') }}</span>
      <div v-if="snapshot && status !== 'ok'" class="text-gray-500" :title="errorDetail">{{ t(`admin.accounts.balance.${status}`) }}<span v-if="snapshot?.http_status"> ({{ snapshot.http_status }})</span></div>
      <div v-if="paused" class="text-red-600">{{ t('admin.accounts.balance.paused') }}</div>
      <div v-else-if="fresh && snapshot?.amounts?.some(isLow)" class="text-amber-600">{{ t('admin.accounts.balance.low') }}</div>
      <div v-if="snapshot?.received_at" class="text-[10px] text-gray-400" :title="t('admin.accounts.balance.updatedAt')">{{ new Date(snapshot.received_at).toLocaleString() }}</div>
    </div>
    <button type="button" class="flex h-6 w-6 shrink-0 items-center justify-center rounded hover:bg-gray-100 dark:hover:bg-dark-700" :disabled="busy" :aria-label="t('admin.accounts.balance.refresh')" :title="t('admin.accounts.balance.refresh')" @click="refresh">
      <Icon name="refresh" size="xs" :class="{ 'animate-spin': busy }" />
    </button>
    <button type="button" class="flex h-6 w-6 shrink-0 items-center justify-center rounded hover:bg-gray-100 dark:hover:bg-dark-700" :aria-label="t('admin.accounts.balance.config')" :title="t('admin.accounts.balance.config')" @click="openConfig">
      <Icon name="cog" size="xs" />
    </button>
    <BaseDialog :show="showConfig" :title="t('admin.accounts.balance.config')" @close="showConfig = false">
      <form class="space-y-4" @submit.prevent="save">
        <p class="break-all text-sm text-gray-500">{{ account.name }}</p>
        <label class="flex items-center gap-2"><input v-model="config.enabled" type="checkbox" @change="config.pause_on_exhaustion && !config.enabled && (config.pause_on_exhaustion = false)" />{{ t('admin.accounts.balance.enabled') }}</label>
        <label class="block text-sm">{{ t('admin.accounts.balance.provider') }}
          <select v-model="config.provider" class="input mt-1 w-full"><option value="auto">{{ t('admin.accounts.balance.auto') }}</option><option value="sub2api">Sub2API</option><option value="newapi">NewAPI</option></select>
        </label>
        <div v-if="config.provider !== 'sub2api'" class="grid grid-cols-2 gap-3">
          <label class="block text-sm">{{ t('admin.accounts.balance.currency') }}<input v-model="config.currency" class="input mt-1 w-full" maxlength="3" pattern="[A-Z]{3}" required /></label>
          <label class="block text-sm">{{ t('admin.accounts.balance.conversion') }}<input v-model.number="config.quota_per_unit" class="input mt-1 w-full" type="number" min="0.000001" max="1000000000000" step="any" required /></label>
        </div>
        <label class="flex items-start gap-2"><input v-model="config.pause_on_exhaustion" type="checkbox" :disabled="!config.enabled" class="mt-1" />{{ t('admin.accounts.balance.pauseOnExhaustion') }}</label>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
        <button type="submit" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
      </form>
    </BaseDialog>
    <span v-if="error && !showConfig" role="alert" class="text-xs text-red-600" :title="error">{{ t('admin.accounts.balance.requestFailed') }}</span>
  </div>
  <span v-else class="text-gray-400">-</span>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIntervalFn } from '@vueuse/core'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { accountsAPI } from '@/api/admin/accounts'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account, UpstreamBalanceAmount, UpstreamBalanceConfig, UpstreamBalanceState } from '@/types'

const props = defineProps<{ account: Account; now: number; lowThreshold: number }>()
const emit = defineEmits<{ updated: [state: UpstreamBalanceState] }>()
const { t } = useI18n()
const defaults: UpstreamBalanceConfig = { enabled: false, provider: 'auto', currency: 'USD', quota_per_unit: 500000, pause_on_exhaustion: false }
const state = computed(() => props.account.extra?.upstream_balance_probe)
const snapshot = computed(() => state.value?.snapshot)
const fresh = computed(() => snapshot.value?.status === 'ok' && Date.parse(snapshot.value.fresh_until ?? '') > props.now)
const status = computed(() => snapshot.value?.status === 'ok' ? (fresh.value ? 'ok' : 'stale') : snapshot.value?.status ?? 'unknown')
const errorDetail = computed(() => snapshot.value?.last_error ? t(`admin.accounts.balance.errors.${snapshot.value.last_error}`) : '')
const paused = computed(() => state.value?.enabled && state.value.pause_on_exhaustion && fresh.value && snapshot.value?.amounts?.some(a => !a.unlimited && a.remaining != null && a.remaining <= 0 && (!a.reset_at || Date.parse(a.reset_at) > props.now)))
const busy = ref(false)
const saving = ref(false)
const error = ref('')
const showConfig = ref(false)
const config = ref<UpstreamBalanceConfig>({ ...defaults })
function isLow(amount: UpstreamBalanceAmount) {
  return fresh.value && !amount.unlimited && amount.remaining != null && amount.remaining <= props.lowThreshold && (!amount.reset_at || Date.parse(amount.reset_at) > props.now)
}
function formatAmount(amount: UpstreamBalanceAmount) {
  return amount.remaining == null ? t('admin.accounts.balance.unknown') : `${amount.remaining.toLocaleString(undefined, { maximumFractionDigits: 4 })} ${amount.currency}`
}
function openConfig() {
  config.value = { ...defaults, ...state.value }
  error.value = ''
  showConfig.value = true
}
async function save() {
  saving.value = true
  error.value = ''
  const { enabled, provider, currency, quota_per_unit, pause_on_exhaustion } = config.value
  try {
    emit('updated', await accountsAPI.updateBalanceConfig(props.account.id, { enabled, provider, currency, quota_per_unit, pause_on_exhaustion }))
    showConfig.value = false
  } catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.accounts.balance.requestFailed')) }
  finally { saving.value = false }
}
async function refresh() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const result = await accountsAPI.probeBalance(props.account.id)
    if (result.state) emit('updated', result.state)
  } catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.accounts.balance.requestFailed')) }
  finally { busy.value = false }
}
useIntervalFn(async () => {
  if (props.account.type !== 'apikey' || busy.value || saving.value || document.hidden) return
  try { emit('updated', await accountsAPI.getBalance(props.account.id)) } catch { /* Keep the last cached state when polling fails. */ }
}, 60_000)
</script>
