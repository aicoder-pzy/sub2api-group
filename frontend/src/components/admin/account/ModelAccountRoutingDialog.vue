<template>
  <BaseDialog :show="true" :title="t('admin.accounts.modelRouting.title')" width="wide" :close-on-escape="!saving" :show-close-button="!saving" @close="emit('close')">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
      <span>{{ t('admin.accounts.modelRouting.scope') }}</span>
      <span>{{ t('admin.accounts.modelRouting.fallback') }}</span>
    </div>
    <div v-if="loading" role="status" class="py-8 text-center">{{ t('common.loading') }}</div>
    <div v-else-if="loaded" class="mt-4 grid min-w-0 gap-4 sm:grid-cols-[13rem_minmax(0,1fr)]">
      <div class="min-w-0 border-b border-gray-200 pb-3 sm:border-b-0 sm:border-r sm:pr-4 dark:border-dark-600">
        <button type="button" class="btn btn-secondary w-full" :disabled="saving || rules.length >= 256" @click="addRule">
          <Icon name="plus" size="sm" />{{ t('admin.accounts.modelRouting.add') }}
        </button>
        <div class="mt-3 max-h-80 overflow-auto">
          <button v-for="(rule, index) in rules" :key="index" type="button" class="flex w-full min-w-0 flex-col gap-1 rounded-md px-3 py-2 text-left text-sm" :class="index === activeIndex ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300' : 'hover:bg-gray-100 dark:hover:bg-dark-700'" :disabled="saving" @click="activeIndex = index">
            <span class="w-full break-all font-medium">{{ rule.model || t('admin.accounts.modelRouting.newModel') }}</span>
            <span class="text-xs text-gray-500">{{ t('admin.accounts.modelRouting.count', { count: rule.account_ids.length }) }}</span>
          </button>
        </div>
      </div>
      <div v-if="activeRule" class="min-w-0 space-y-4">
        <div class="flex items-end gap-2">
          <label class="min-w-0 flex-1 text-sm font-medium">
            {{ t('admin.accounts.modelRouting.model') }}
            <input v-model="activeRule.model" class="input mt-1 w-full" maxlength="200" autocomplete="off" :disabled="saving" :placeholder="t('admin.accounts.modelRouting.modelPlaceholder')" />
          </label>
          <button type="button" class="btn btn-secondary h-10 w-10 shrink-0 !p-0" :title="t('admin.accounts.modelRouting.remove')" :aria-label="t('admin.accounts.modelRouting.remove')" :disabled="saving" @click="removeRule">
            <Icon name="trash" size="sm" />
          </button>
        </div>
        <div>
          <div class="mb-2 text-sm font-medium">{{ t('admin.accounts.modelRouting.accounts') }}</div>
          <div class="flex max-h-32 flex-wrap gap-1.5 overflow-auto">
            <span v-for="id in activeRule.account_ids" :key="id" class="inline-flex min-w-0 max-w-full items-center gap-1 rounded-md bg-primary-50 px-2 py-1 text-xs text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">
              <span class="min-w-0 break-all">{{ accountNames[id] || `#${id}` }} <span v-if="accountNames[id]">#{{ id }}</span></span>
              <button type="button" class="shrink-0 p-1" :title="t('admin.accounts.modelRouting.removeAccount')" :aria-label="`${t('admin.accounts.modelRouting.removeAccount')} #${id}`" :disabled="saving" @click="toggleAccount(id)"><Icon name="x" size="xs" /></button>
            </span>
            <span v-if="!activeRule.account_ids.length" class="text-xs text-red-600 dark:text-red-400">{{ t('admin.accounts.modelRouting.blocked') }}</span>
          </div>
        </div>
        <label class="block text-sm">
          {{ t('admin.accounts.modelRouting.search') }}
          <input v-model="search" type="search" class="input mt-1 w-full" :disabled="saving" />
        </label>
        <div class="h-56 overflow-auto border-y border-gray-200 dark:border-dark-600" :aria-busy="searching">
          <p v-if="searching" role="status" class="p-3 text-sm text-gray-500">{{ t('common.loading') }}</p>
          <template v-else>
            <label v-for="account in accounts" :key="account.id" class="flex cursor-pointer items-center gap-3 border-b border-gray-100 px-2 py-2 last:border-0 dark:border-dark-700">
              <input type="checkbox" class="h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600" :checked="activeRule.account_ids.includes(account.id)" :disabled="saving || (activeRule.account_ids.length >= 4096 && !activeRule.account_ids.includes(account.id))" @change="toggleAccount(account.id)" />
              <span class="min-w-0 flex-1 break-all text-sm">{{ account.name }} <span class="text-xs text-gray-500">#{{ account.id }}</span></span>
              <span class="shrink-0 text-xs text-gray-500">{{ account.platform }}</span>
              <span class="shrink-0 text-xs" :class="account.status === 'active' && account.schedulable ? 'text-green-600' : 'text-gray-500'">{{ account.status === 'active' && account.schedulable ? t('admin.accounts.modelRouting.available') : t('admin.accounts.modelRouting.unavailable') }}</span>
            </label>
            <p v-if="!accounts.length" class="p-3 text-sm text-gray-500">{{ t('admin.accounts.modelRouting.noAccounts') }}</p>
          </template>
        </div>
        <div class="flex items-center justify-between gap-2 text-xs text-gray-500">
          <span>{{ t('admin.accounts.modelRouting.total', { count: total }) }}</span>
          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-secondary h-8 w-8 !p-0" :title="t('common.back')" :aria-label="t('common.back')" :disabled="searching || saving || page <= 1" @click="page--"><Icon name="chevronLeft" size="sm" /></button>
            <span>{{ page }} / {{ Math.max(1, Math.ceil(total / 20)) }}</span>
            <button type="button" class="btn btn-secondary h-8 w-8 !p-0" :title="t('common.next')" :aria-label="t('common.next')" :disabled="searching || saving || page * 20 >= total" @click="page++"><Icon name="chevronRight" size="sm" /></button>
          </div>
        </div>
      </div>
      <p v-else class="py-8 text-center text-sm text-gray-500">{{ t('admin.accounts.modelRouting.unrestricted') }}</p>
    </div>
    <p v-if="error" role="alert" class="mt-3 break-words text-sm text-red-600">{{ error }}</p>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button v-if="!loaded && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('common.tryAgain') }}</button>
      <button type="button" class="btn btn-primary" :disabled="loading || !loaded || saving" @click="save"><Icon name="check" size="sm" />{{ t(saving ? 'common.saving' : 'common.save') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import { getModelAccountRouting, updateModelAccountRouting, type ModelAccountRoutingRule } from '@/api/admin/settings'
import type { AccountListItem } from '@/types'

const props = defineProps<{ selectedIds?: number[] }>()
const emit = defineEmits<{ close: []; updated: [rules: ModelAccountRoutingRule[]] }>()
const { t } = useI18n()
const rules = ref<ModelAccountRoutingRule[]>([])
const activeIndex = ref(0)
const activeRule = computed(() => rules.value[activeIndex.value])
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const accounts = ref<AccountListItem[]>([])
const accountNames = ref<Record<number, string>>({})
const search = ref('')
const page = ref(1)
const total = ref(0)
const searching = ref(false)
let controller: AbortController | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

function errorMessage(cause: unknown): string {
  const detail = cause as { response?: { data?: { message?: string } }; message?: string }
  return detail?.response?.data?.message || detail?.message || t('common.unknownError')
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const settings = await getModelAccountRouting()
    if (disposed) return
    rules.value = settings.rules
    loaded.value = true
    await searchAccounts()
  } catch (cause) { error.value = errorMessage(cause) }
  finally { loading.value = false }
}

async function searchAccounts() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  searching.value = true
  try {
    const result = await listAccounts(page.value, 20, { search: search.value, lite: 'true' }, { signal: current.signal })
    if (current.signal.aborted || disposed) return
    accounts.value = result.items
    total.value = result.total
    for (const account of result.items) accountNames.value[account.id] = account.name
  } catch (cause) {
    if (!current.signal.aborted && !disposed) error.value = errorMessage(cause)
  } finally { if (controller === current) searching.value = false }
}

watch(search, () => {
  controller?.abort()
  controller = undefined
  searching.value = true
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (page.value !== 1) page.value = 1
    else void searchAccounts()
  }, 250)
})
watch(page, () => void searchAccounts())
onMounted(load)
onBeforeUnmount(() => { disposed = true; controller?.abort(); clearTimeout(searchTimer) })

function addRule() {
  rules.value.push({ model: '', account_ids: [...(props.selectedIds || [])] })
  activeIndex.value = rules.value.length - 1
}
function removeRule() {
  rules.value.splice(activeIndex.value, 1)
  activeIndex.value = Math.max(0, Math.min(activeIndex.value, rules.value.length - 1))
}
function toggleAccount(id: number) {
  const rule = activeRule.value
  if (!rule) return
  const index = rule.account_ids.indexOf(id)
  if (index >= 0) rule.account_ids.splice(index, 1)
  else rule.account_ids.push(id)
}

async function save() {
  if (saving.value || !loaded.value) return
  error.value = ''
  const normalized = rules.value.map(rule => ({ model: rule.model.trim(), account_ids: [...rule.account_ids] }))
  const models = new Set<string>()
  for (const rule of normalized) {
    if (!rule.model || /[\s*?]/u.test(rule.model) || models.has(rule.model)) {
      error.value = t('admin.accounts.modelRouting.invalidModel')
      return
    }
    models.add(rule.model)
  }
  saving.value = true
  try {
    const settings = await updateModelAccountRouting({ rules: normalized })
    emit('updated', settings.rules)
    emit('close')
  } catch (cause) { error.value = errorMessage(cause) }
  finally { saving.value = false }
}
</script>
