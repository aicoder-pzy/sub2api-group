<template>
  <AppLayout>
    <div class="upstream-console">
      <header class="console-heading">
        <div><h1>{{ tr('title') }}</h1><p>{{ tr('description') }}</p></div>
        <div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary" :disabled="working || !sites.length" @click="syncAll"><Icon name="refresh" size="sm" :class="{ 'animate-spin': syncing }" />{{ syncing ? tr('syncing') : tr('syncAll') }}</button><button type="button" class="btn btn-primary" :disabled="working" @click="openAdd"><Icon name="plus" size="sm" />{{ tr('add') }}</button></div>
      </header>
      <div v-if="error" role="alert" class="console-alert">{{ error }}</div>
      <div v-if="notice" role="status" class="console-notice">{{ notice }}</div>
      <div class="console-summary">
        <div v-for="stat in summary" :key="stat.label"><span class="stat-symbol" :class="stat.tone"><Icon :name="stat.icon" size="md" /></span><div><span>{{ tr(stat.label) }}</span><strong>{{ stat.value }}</strong></div></div>
      </div>
      <div v-if="!sites.length && !busy" class="console-empty console-panel"><Icon name="globe" size="xl" /><h2>{{ tr('noSites') }}</h2><p>{{ tr('noSitesHint') }}</p><button class="btn btn-primary" type="button" @click="openAdd">{{ tr('add') }}</button></div>
      <div v-else class="console-grid">
        <aside class="console-panel site-list">
          <div class="site-list-tools"><input v-model="siteSearch" class="input" :placeholder="tr('searchSites')" :aria-label="tr('searchSites')" /><label class="mt-3 flex items-center gap-2 text-xs text-gray-500"><input v-model="attentionOnly" type="checkbox" />{{ tr('attentionOnly') }}</label></div>
          <div class="site-list-items">
            <button v-for="item in filteredSites" :key="item.id" type="button" class="site-item" :class="{ selected: item.id === selectedId }" :disabled="working" :aria-pressed="item.id === selectedId" @click="selectedId = item.id">
              <span class="site-avatar">{{ item.name.slice(0, 1).toUpperCase() }}</span>
              <span class="min-w-0 flex-1"><strong class="block truncate">{{ item.name }}</strong><span class="block truncate text-xs text-gray-400">{{ domain(item.base_url) }}</span><span class="mt-1 block text-xs text-gray-500">{{ item.kind === 'newapi' ? 'New API' : 'Sub2API' }} · {{ item.account_count }} {{ tr('associated') }}</span></span>
              <span class="site-dot" :class="status(item)" :title="tr(status(item))"></span>
            </button>
            <p v-if="!filteredSites.length" class="p-5 text-sm text-gray-500">{{ tr('noMatch') }}</p>
          </div>
        </aside>
        <section v-if="selected" class="console-panel site-detail">
          <div class="site-detail-heading">
            <div class="flex min-w-0 items-center gap-3"><span class="site-avatar large">{{ selected.name.slice(0, 1).toUpperCase() }}</span><div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><h2 class="break-words">{{ selected.name }}</h2><span class="site-badge">{{ selected.kind === 'newapi' ? 'New API' : 'Sub2API' }}</span><span class="site-badge" :class="status(selected)"><span class="site-dot" :class="status(selected)"></span>{{ tr(status(selected)) }}</span></div><p class="mt-1 break-all text-xs text-gray-500">{{ selected.base_url }}</p></div></div>
            <div class="flex shrink-0 gap-2"><button type="button" class="btn btn-secondary" :disabled="working" @click="tab = 'settings'"><Icon name="cog" size="sm" />{{ tr('settings') }}</button><button type="button" class="btn btn-secondary" :disabled="working" @click="syncSelected"><Icon name="refresh" size="sm" />{{ tr('sync') }}</button></div>
          </div>
          <div class="site-metrics">
            <div><span>{{ tr('wallet') }}</span><strong data-site-wallet>{{ amount(selected.inventory?.wallet) }}</strong><small>{{ selected.inventory?.errors.wallet ? tr('stale') + ' · ' : '' }}{{ selected.inventory?.wallet_at ? formatTime(selected.inventory.wallet_at) : tr('walletHint') }}</small></div>
            <div><span>{{ tr('availableKeys') }}</span><strong>{{ selected.inventory ? keys.filter(key => key.available).length : '—' }}<small v-if="selected.inventory"> / {{ keys.length }}</small></strong><small>{{ tr('updated') }} · {{ formatTime(selected.inventory?.received_at) }}</small></div>
            <div><span>{{ tr('associated') }}</span><strong>{{ selected.account_count }}</strong><small>{{ tr('localAccounts') }}</small></div>
          </div>
          <div v-if="selected.inventory && Object.keys(selected.inventory.errors).length" class="mx-5 mb-4 rounded-lg bg-amber-50 px-4 py-3 text-xs text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">
            <details><summary class="cursor-pointer">{{ tr('syncPartial') }} · {{ tr('connectionErrors') }}</summary><p v-for="(message, component) in selected.inventory.errors" :key="component" class="mt-2 break-words">{{ componentLabel(component) }}：{{ message }}</p></details>
          </div>
          <div class="console-tabs" role="tablist" :aria-label="tr('title')"><button v-for="item in tabs" :id="`site-tab-${item}`" :key="item" type="button" role="tab" :aria-selected="tab === item" :tabindex="tab === item ? 0 : -1" aria-controls="site-tab-panel" :class="{ active: tab === item }" @click="tab = item" @keydown="tabKeydown($event, item)">{{ tr(item) }}<span v-if="item === 'keys'">{{ keys.length }}</span></button></div>
          <div id="site-tab-panel" role="tabpanel" :aria-labelledby="`site-tab-${tab}`" class="min-w-0">
            <template v-if="tab === 'keys'">
              <div class="key-toolbar"><input v-model="keySearch" class="input key-search" :placeholder="tr('keySearch')" :aria-label="tr('keySearch')" /><select v-model="importFilter" class="input" :aria-label="tr('allImports')"><option value="all">{{ tr('allImports') }}</option><option value="yes">{{ tr('importedFilter') }}</option><option value="no">{{ tr('unimportedFilter') }}</option></select><select v-model="availabilityFilter" class="input" :aria-label="tr('allStatuses')"><option value="all">{{ tr('allStatuses') }}</option><option value="yes">{{ tr('available') }}</option><option value="no">{{ tr('unavailable') }}</option></select></div>
              <div class="key-batchbar"><p>{{ t('upstreamSites.selected', { count: selectedKeys.length }) }}</p><div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary" :disabled="working || !selected.inventory?.groups.length" @click="showCreate = true">{{ tr('createKey') }}</button><button type="button" class="btn btn-primary" :disabled="working || !selectedKeys.length || !!accountsError" @click="openImport(selectedKeys)">{{ tr('batch') }}</button></div></div>
              <div v-if="accountsError" role="alert" class="mx-5 mb-3 flex flex-wrap gap-2 text-sm text-red-600">{{ accountsError }}<button type="button" class="text-primary-600 underline" @click="loadAccounts">{{ tr('refresh') }}</button></div>
              <div class="console-table-scroll" tabindex="0" :aria-label="tr('keys')">
                <table class="console-table"><thead><tr><th class="w-9"><input type="checkbox" :checked="pageKeys.length > 0 && pageKeys.filter(key => key.available).length > 0 && pageKeys.filter(key => key.available).every(key => selectedKeys.includes(key.id))" :aria-label="tr('selectPage')" :disabled="working" @change="selectPage(($event.target as HTMLInputElement).checked)" /></th><th>{{ tr('keyName') }}</th><th>{{ tr('models') }}</th><th>{{ tr('quota') }}</th><th>{{ tr('account') }}</th><th>{{ tr('status') }}</th><th>{{ tr('actions') }}</th></tr></thead>
                  <tbody><tr v-for="key in pageKeys" :key="key.id"><td><input v-model="selectedKeys" type="checkbox" :value="key.id" :disabled="working || !key.available" :aria-label="t('upstreamSites.selectKey', { name: key.name })" /></td><td><strong>{{ key.name }}</strong><p class="table-muted">{{ key.group_name || key.group_id || '—' }}<span v-if="groupRatio(key.group_id) !== undefined"> · ×{{ groupRatio(key.group_id) }}</span></p><p class="table-muted font-mono">{{ key.suffix ? '••••' + key.suffix : '#' + key.id }}</p></td>
                    <td><template v-if="selected.catalogs?.[key.id]"><span v-for="model in selected.catalogs[key.id]!.models.slice(0, 2)" :key="model" class="model-chip">{{ model }}</span><small v-if="selected.catalogs[key.id]!.models.length > 2" class="block text-gray-400">+{{ selected.catalogs[key.id]!.models.length - 2 }}</small><p class="table-muted">{{ formatTime(selected.catalogs[key.id]!.attempt_at) }}</p></template><span v-else class="text-xs text-gray-400">{{ tr('notRead') }}</span><button type="button" class="mt-1 block text-xs text-primary-600 dark:text-primary-400" :disabled="working || !key.available" @click="readModels(key.id)">{{ tr('readModels') }}</button></td>
                    <td class="tabular-nums">{{ amount(key.quota, true) }}</td><td><RouterLink v-for="account in keyAccounts(key.id)" :key="account.id" :to="accountLink(account.id)" class="block max-w-40 truncate text-primary-600 dark:text-primary-400" :title="account.name">#{{ account.id }} · {{ account.name }}</RouterLink><span v-if="!keyAccounts(key.id).length" class="text-gray-400">{{ accountsLoading ? tr('loading') : tr('unassociated') }}</span></td><td><span class="site-badge" :class="key.available ? 'ok' : 'attention'">{{ tr(key.status || (key.available ? 'active' : 'inactive')) }}</span></td><td><button type="button" class="text-primary-600 dark:text-primary-400" :disabled="working || !key.available || !!accountsError || accountsLoading" @click="openImport([key.id])">{{ tr('import') }}</button></td></tr>
                    <tr v-if="!pageKeys.length"><td colspan="7" class="py-12 text-center text-gray-400">{{ keys.length ? tr('noMatch') : tr('emptyKeys') }}</td></tr>
                  </tbody></table>
              </div>
              <div class="console-pagination"><span>{{ t('upstreamSites.page', { page, pages, count: filteredKeys.length }) }}</span><div class="flex gap-2"><button class="btn btn-secondary" :disabled="page <= 1" @click="page--">{{ tr('previous') }}</button><button class="btn btn-secondary" :disabled="page >= pages" @click="page++">{{ tr('next') }}</button></div></div>
            </template>
            <div v-else-if="tab === 'accounts' || tab === 'changes'" class="p-5">
              <div class="mb-5 flex flex-wrap items-center justify-between gap-3"><p class="max-w-xl text-sm leading-6 text-gray-500">{{ tr(tab === 'changes' ? 'changesHint' : 'linkHint') }}</p><button v-if="tab === 'accounts'" class="btn btn-secondary" :disabled="working" @click="showLink = true">{{ tr('link') }}</button></div>
              <p v-if="accountsError" role="alert" class="mb-3 text-sm text-red-600">{{ accountsError }} <button @click="loadAccounts">{{ tr('refresh') }}</button></p>
              <p v-if="!accounts.length" class="py-12 text-center text-sm text-gray-400">{{ accountsLoading ? tr('loading') : tr('noAccounts') }}</p>
              <section v-for="account in accounts" :key="account.id" class="account-card">
                <div class="flex flex-wrap items-center justify-between gap-3"><div class="min-w-0"><RouterLink :to="accountLink(account.id)" class="break-words font-medium text-primary-700 dark:text-primary-400">#{{ account.id }} · {{ account.name }}</RouterLink><p class="mt-1 text-xs text-gray-500">{{ account.platform }} · {{ tr(account.source) }} · Key #{{ account.key_id }} · {{ Object.keys(account.models || {}).length }} {{ tr('models') }}</p></div><div class="flex flex-wrap gap-3"><button v-if="tab === 'changes'" class="btn btn-secondary" :disabled="working" @click="previewModels(account.id)">{{ tr('preview') }}</button><RouterLink v-else :to="accountLink(account.id)" class="text-sm text-primary-600">{{ tr('viewAccount') }}</RouterLink><button v-if="tab === 'accounts' && account.source === 'linked'" class="text-sm text-gray-500" :disabled="working" @click="unlink(account)">{{ tr('unlink') }}</button></div></div>
                <div v-if="tab === 'changes' && previews[account.id]" class="model-preview">
                  <div class="grid gap-4 sm:grid-cols-2"><div><h4 class="text-sm font-medium text-primary-700 dark:text-primary-400">+ {{ tr('added') }} · {{ previews[account.id]!.added.length }}</h4><p class="mt-2 break-words text-xs">{{ previews[account.id]!.added.join('、') || '—' }}</p></div><div><h4 class="text-sm font-medium text-amber-700 dark:text-amber-400">− {{ tr('removed') }} · {{ previews[account.id]!.removed.length }}</h4><p class="mt-2 break-words text-xs">{{ previews[account.id]!.removed.join('、') || '—' }}</p></div></div>
                  <details class="my-4 text-xs text-gray-500"><summary class="cursor-pointer">{{ tr('currentScope') }}</summary><p v-for="(target, source) in previews[account.id]!.mapping" :key="source" class="mt-1 break-all font-mono">{{ source }} → {{ target }}</p></details>
                  <button v-if="previews[account.id]!.added.length || previews[account.id]!.removed.length" class="btn btn-primary" :disabled="working" @click="applyModels(account.id)">{{ tr('apply') }}</button><p v-else class="text-sm text-gray-500">{{ tr('noChanges') }}</p>
                </div>
              </section>
            </div>
            <div v-else class="p-5">
              <p v-if="selected.kind === 'sub2api'" class="mb-4 text-sm" :class="selected.can_refresh ? 'text-primary-600' : 'text-amber-600'" data-site-authorization>{{ tr(selected.can_refresh ? 'automaticRefresh' : 'manualRefresh') }}</p>
              <UpstreamSiteForm v-model="settingsForm" :busy="working" :proxies="proxies" @save="saveSelected"><button type="button" class="btn btn-secondary text-red-600" :disabled="working" @click="showDelete = true">{{ tr('delete') }}</button></UpstreamSiteForm>
              <div class="mt-7 border-t border-gray-100 pt-5 dark:border-dark-700"><h3 class="mb-3 text-sm font-medium">{{ tr('checks') }}</h3><p class="mb-3 text-xs text-gray-500">{{ formatTime(selected.inventory?.attempt_at) }}</p><div class="space-y-2"><p v-for="component in ['wallet', 'groups', 'keys']" :key="component" class="break-words text-xs"><span class="mr-2 inline-block h-1.5 w-1.5 rounded-full" :class="!selected.inventory ? 'bg-gray-400' : selected.inventory.errors[component] ? 'bg-amber-500' : 'bg-primary-500'"></span>{{ componentLabel(component) }} · {{ !selected.inventory ? tr('unknown') : selected.inventory.errors[component] || tr('ok') }}</p><p class="text-xs text-gray-500">{{ tr('modelsCheck') }} · {{ Object.keys(selected.catalogs || {}).length }} Key</p></div></div>
            </div>
          </div>
        </section>
      </div>
      <p class="mt-4 text-xs text-gray-400">{{ tr('readOnlyHint') }}</p>
    </div>
    <BaseDialog :show="showAdd" :title="tr('add')" :close-on-escape="!working" :show-close-button="!working" width="wide" @close="closeAdd"><p v-if="error" class="mb-3 text-sm text-red-600" role="alert">{{ error }}</p><UpstreamSiteForm v-model="addForm" :busy="working" is-new :proxies="proxies" @save="saveNew" /></BaseDialog>
    <BaseDialog :show="showDelete" :title="tr('delete')" width="narrow" @close="showDelete = false"><p class="text-sm text-gray-500">{{ tr('deleteConfirm') }}</p><template #footer><button class="btn btn-secondary" @click="showDelete = false">{{ tr('cancel') }}</button><button class="btn btn-primary" :disabled="working" @click="deleteSite">{{ tr('confirm') }}</button></template></BaseDialog>
    <BaseDialog :show="showLink" :title="tr('link')" :close-on-escape="!working" :show-close-button="!working" @close="showLink = false"><form class="space-y-4" @submit.prevent="linkAccount"><p class="text-sm leading-6 text-gray-500">{{ tr('linkHint') }}</p><p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><label class="block"><span class="input-label">{{ tr('accountId') }}</span><input v-model.number="linkAccountId" type="number" required min="1" class="input" /></label><label class="block"><span class="input-label">{{ tr('keys') }}</span><select v-model="linkKeyId" required class="input" :aria-label="tr('keys')"><option value="">{{ tr('choose') }}</option><option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name }} · #{{ key.id }}</option></select></label><button class="btn btn-primary" :disabled="working" type="submit">{{ tr('confirm') }}</button></form></BaseDialog>
    <BaseDialog :show="showCreate" :title="tr('createKey')" :close-on-escape="!working" :show-close-button="!working" @close="showCreate = false; createConfirmed = false"><form class="space-y-4" @submit.prevent="createKey"><p class="text-sm leading-6 text-amber-700 dark:text-amber-400">{{ tr('createHint') }}</p><p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><label class="block"><span class="input-label">{{ tr('newKeyName') }}</span><input v-model="newKeyName" required maxlength="100" class="input" /></label><label class="block"><span class="input-label">{{ tr('upstreamGroup') }}</span><select v-model="newKeyGroup" required class="input" :aria-label="tr('upstreamGroup')"><option value="">{{ tr('choose') }}</option><option v-for="group in selected?.inventory?.groups || []" :key="group.id" :value="group.id">{{ group.name }}</option></select></label><label class="flex items-center gap-2 text-sm"><input v-model="createConfirmed" type="checkbox" />{{ tr('createConfirm') }}</label><button type="submit" class="btn btn-primary" :disabled="working || !createConfirmed">{{ tr('createKey') }}</button></form></BaseDialog>
    <UpstreamImportDrawer v-if="selected" :show="showImport" :site="selected" :keys="importKeys" :groups="groups" :accounts="accounts" @close="showImport = false" @imported="refreshAccounts" @models="storeModels" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import UpstreamSiteForm from '@/components/admin/account/UpstreamSiteForm.vue'
import UpstreamImportDrawer from '@/components/admin/account/UpstreamImportDrawer.vue'
import * as groupsAPI from '@/api/admin/groups'
import * as proxiesAPI from '@/api/admin/proxies'
import type { AdminGroup, Proxy } from '@/types'
import { upstreamSitesAPI as api, siteError, type UpstreamSite, type SiteAccount, type SiteForm, type SiteAmount, type ModelPreview } from '@/api/admin/upstreamSites'

const { t, locale } = useI18n()
const tr = (key: string) => t(`upstreamSites.${key}`)
const sites = ref<UpstreamSite[]>([]), groups = ref<AdminGroup[]>([]), proxies = ref<Proxy[]>([])
const selectedId = ref(''), siteSearch = ref(''), attentionOnly = ref(false), keySearch = ref(''), importFilter = ref('all'), availabilityFilter = ref('all'), page = ref(1)
const tabs = ['keys', 'accounts', 'changes', 'settings'] as const
const tab = ref<typeof tabs[number]>('keys')
const busy = ref(false), syncing = ref(false), error = ref(''), notice = ref('')
const working = computed(() => busy.value || syncing.value)
const accounts = ref<SiteAccount[]>([]), accountsLoading = ref(false), accountsError = ref('')
const selectedKeys = ref<string[]>([]), importKeys = ref<NonNullable<UpstreamSite['inventory']>['keys']>([])
const showAdd = ref(false), showDelete = ref(false), showLink = ref(false), showCreate = ref(false), showImport = ref(false)
const linkAccountId = ref<number>(), linkKeyId = ref(''), newKeyName = ref(''), newKeyGroup = ref(''), createConfirmed = ref(false)
const previews = ref<Record<number, ModelPreview>>({})
const emptyForm = (): SiteForm => ({ name: '', kind: 'sub2api', base_url: '', inference_url: '', purchase_note: '', user_id: 0, proxy_id: null, revision: 0, access_token: '' })
const settingsForm = ref(emptyForm()), addForm = ref(emptyForm())
const selected = computed(() => sites.value.find(site => site.id === selectedId.value))
const keys = computed(() => selected.value?.inventory?.keys || [])
const status = (site: UpstreamSite) => site.inventory?.status || 'unknown'
const filteredSites = computed(() => sites.value.filter(site => (!attentionOnly.value || status(site) === 'attention') && `${site.name} ${site.base_url}`.toLowerCase().includes(siteSearch.value.toLowerCase())))
const summary = computed(() => [
  { label: 'total', value: sites.value.length, icon: 'globe', tone: '' },
  { label: 'healthy', value: sites.value.filter(site => status(site) === 'ok').length, icon: 'checkCircle', tone: 'green' },
  { label: 'attention', value: sites.value.filter(site => status(site) === 'attention').length, icon: 'exclamationTriangle', tone: 'amber' },
  { label: 'localAccounts', value: sites.value.reduce((sum, site) => sum + site.account_count, 0), icon: 'users', tone: '' }
] as const)
const keyAccounts = (keyId: string) => accounts.value.filter(account => account.key_id === keyId)
const filteredKeys = computed(() => keys.value.filter(key => {
  const linked = keyAccounts(key.id)
  return `${key.name} ${key.group_name} ${key.group_id} ${key.id} ${linked.map(account => `${account.id} ${account.name}`).join(' ')}`.toLowerCase().includes(keySearch.value.toLowerCase()) && (importFilter.value === 'all' || (importFilter.value === 'yes') === !!linked.length) && (availabilityFilter.value === 'all' || (availabilityFilter.value === 'yes') === key.available)
}))
const pages = computed(() => Math.max(1, Math.ceil(filteredKeys.value.length / 15)))
const pageKeys = computed(() => filteredKeys.value.slice((page.value - 1) * 15, page.value * 15))
const groupRatio = (id: string) => selected.value?.inventory?.groups.find(group => group.id === id)?.multiplier
const accountLink = (id: number) => ({ path: '/admin/accounts', query: { account_id: String(id) } })
const domain = (url: string) => { try { return new URL(url).host } catch { return url } }
const componentLabel = (component: string | number) => tr(({ wallet: 'walletCheck', groups: 'groupsCheck', keys: 'keysCheck' } as Record<string, string>)[component] || 'modelsCheck')
function formatTime(value?: string) {
  if (!value) return tr('never')
  return new Date(value).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
function amount(value?: SiteAmount, quota = false) {
  if (!value) return quota ? '—' : tr('unknownBalance')
  if (value.unlimited) return tr('unlimited')
  if (value.remaining === null || value.remaining === undefined) return '—'
  const number = value.remaining.toLocaleString(locale.value, { maximumFractionDigits: value.currency === 'QUOTA' ? 0 : 2, minimumFractionDigits: value.currency === 'QUOTA' ? 0 : 2 })
  return value.currency === 'QUOTA' ? `${number} ${tr('quotaUnit')}` : `${value.currency} ${number}`
}
async function run(action: () => Promise<void>) {
  if (working.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await action() } catch (cause) { error.value = siteError(cause) }
  finally { busy.value = false }
}
async function loadSites() {
  sites.value = await api.list()
  if (!sites.value.some(site => site.id === selectedId.value)) selectedId.value = sites.value[0]?.id || ''
}
async function loadAccounts() {
  const id = selectedId.value
  if (!id) return
  accountsLoading.value = true; accountsError.value = ''
  try { const result = await api.accounts(id); if (id === selectedId.value) accounts.value = result }
  catch { if (id === selectedId.value) accountsError.value = tr('relationFailed') }
  finally { if (id === selectedId.value) accountsLoading.value = false }
}
async function refreshAccounts() { await loadAccounts(); if (selected.value && !accountsError.value) selected.value.account_count = accounts.value.length }
function formFor(site: UpstreamSite): SiteForm { return { name: site.name, kind: site.kind, base_url: site.base_url, inference_url: site.inference_url, purchase_note: site.purchase_note, user_id: site.user_id, proxy_id: site.proxy_id, revision: site.revision, access_token: '' } }
watch(selectedId, () => {
  selectedKeys.value = []; accounts.value = []; previews.value = {}; page.value = 1; error.value = ''; notice.value = ''
  settingsForm.value = selected.value ? formFor(selected.value) : emptyForm()
  void loadAccounts()
})
watch([keySearch, importFilter, availabilityFilter], () => { page.value = 1 })
watch(pages, count => { page.value = Math.min(page.value, count) })
function tabKeydown(event: KeyboardEvent, item: typeof tabs[number]) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const index = tabs.indexOf(item)
  tab.value = tabs[event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length]!
  void nextTick(() => document.getElementById(`site-tab-${tab.value}`)?.focus())
}
function selectPage(checked: boolean) {
  const ids = pageKeys.value.filter(key => key.available).map(key => key.id)
  selectedKeys.value = checked ? [...new Set([...selectedKeys.value, ...ids])] : selectedKeys.value.filter(id => !ids.includes(id))
}
function openAdd() { error.value = ''; addForm.value = emptyForm(); showAdd.value = true }
function closeAdd() { showAdd.value = false; addForm.value.access_token = '' }
function openImport(ids: string[]) { if (accountsLoading.value || accountsError.value) return; importKeys.value = keys.value.filter(key => ids.includes(key.id) && key.available); showImport.value = true }
function storeModels(keyId: string, models: string[]) { if (selected.value) { selected.value.catalogs ||= {}; selected.value.catalogs[keyId] = { models, attempt_at: new Date().toISOString() } } }
async function syncSelected() {
  if (!selected.value) return
  const site = selected.value
  await run(async () => { site.inventory = await api.sync(site.id); selectedKeys.value = []; previews.value = {}; notice.value = tr(site.inventory.status === 'ok' ? 'syncNotice' : 'syncPartial'); await refreshAccounts() })
}
async function syncAll() {
  if (working.value) return
  syncing.value = true; error.value = ''; notice.value = ''
  const failures: string[] = []
  try {
    for (const site of sites.value) {
      try { site.inventory = await api.sync(site.id); if (site.inventory.status !== 'ok') failures.push(site.name) }
      catch (cause) { failures.push(`${site.name}: ${siteError(cause)}`) }
    }
    previews.value = {}; selectedKeys.value = []; await refreshAccounts()
    notice.value = tr(failures.length ? 'syncPartial' : 'syncNotice')
    if (failures.length) error.value = failures.join('；')
  } finally { syncing.value = false }
}
async function saveNew() { await run(async () => { const result = await api.save('', addForm.value); addForm.value.access_token = ''; await loadSites(); selectedId.value = result.id; closeAdd(); notice.value = tr('saved') }) }
async function saveSelected() {
  if (!selected.value) return
  const id = selected.value.id
  await run(async () => { await api.save(id, settingsForm.value); settingsForm.value.access_token = ''; await loadSites(); settingsForm.value = formFor(selected.value!); previews.value = {}; notice.value = tr('saved') })
}
async function deleteSite() { if (selected.value) await run(async () => { await api.delete(selectedId.value); showDelete.value = false; await loadSites() }) }
async function readModels(keyId: string) { await run(async () => { storeModels(keyId, await api.models(selectedId.value, keyId)) }) }
async function linkAccount() { if (selected.value && linkAccountId.value) await run(async () => { await api.link(selectedId.value, selected.value!.revision, linkAccountId.value!, linkKeyId.value); showLink.value = false; await refreshAccounts(); notice.value = tr('linkedSuccess') }) }
async function unlink(account: SiteAccount) { if (selected.value) await run(async () => { await api.link(selectedId.value, selected.value!.revision, account.id, account.key_id, true); delete previews.value[account.id]; await refreshAccounts() }) }
async function previewModels(accountId: number) { delete previews.value[accountId]; await run(async () => { previews.value[accountId] = await api.modelPreview(selectedId.value, selected.value!.revision, accountId); await loadSites() }) }
async function applyModels(accountId: number) { await run(async () => { await api.modelPreview(selectedId.value, selected.value!.revision, accountId, previews.value[accountId]); delete previews.value[accountId]; await refreshAccounts(); notice.value = tr('applied') }) }
async function createKey() {
  if (!createConfirmed.value || !selected.value) return
  createConfirmed.value = false
  await run(async () => { await api.createKey(selectedId.value, selected.value!.revision, newKeyName.value, newKeyGroup.value); showCreate.value = false; notice.value = tr('created'); selected.value!.inventory = await api.sync(selectedId.value) })
}
onMounted(() => { void run(async () => { const results = await Promise.allSettled([loadSites(), groupsAPI.getAll(), proxiesAPI.getAll()]); if (results[0].status === 'rejected') throw results[0].reason; if (results[1].status === 'fulfilled') groups.value = results[1].value; else error.value = siteError(results[1].reason); if (results[2].status === 'fulfilled') proxies.value = results[2].value; else error.value = siteError(results[2].reason) }) })
</script>

<style scoped>
.upstream-console { @apply mx-auto w-full min-w-0 max-w-[1680px] text-gray-800 dark:text-gray-100; }
.console-heading { @apply mb-6 flex flex-wrap items-center justify-between gap-4; }
.console-heading h1 { @apply text-2xl font-semibold tracking-tight; }
.console-heading p { @apply mt-1 text-sm text-gray-500 dark:text-gray-400; }
.console-alert { @apply mb-4 break-words rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300; }
.console-notice { @apply mb-4 rounded-xl bg-primary-50 p-4 text-sm text-primary-800 dark:bg-primary-950/30 dark:text-primary-300; }
.console-panel { @apply min-w-0 rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900; }
.console-summary { @apply mb-6 grid grid-cols-2 gap-y-5 rounded-xl border border-gray-200 bg-white px-5 py-5 dark:border-dark-700 dark:bg-dark-900 md:grid-cols-4; }
.console-summary > div { @apply flex min-w-0 items-center gap-2 md:justify-center md:gap-3; }
.console-summary > div + div { @apply md:border-l md:border-gray-100 md:dark:border-dark-700; }
.console-summary span:not(.stat-symbol) { @apply text-xs text-gray-500 dark:text-gray-400; }
.console-summary strong { @apply mt-1 block text-2xl font-semibold tabular-nums; }
.stat-symbol { @apply flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-gray-50 text-gray-500 dark:bg-dark-800 dark:text-gray-400 md:h-10 md:w-10; }
.stat-symbol.green { @apply bg-primary-50 text-primary-600 dark:bg-primary-950/30 dark:text-primary-400; }
.stat-symbol.amber { @apply bg-amber-50 text-amber-600 dark:bg-amber-950/30 dark:text-amber-400; }
.console-grid { @apply grid min-w-0 gap-5 xl:grid-cols-[252px_minmax(0,1fr)]; }
.site-list { @apply overflow-hidden self-start; }
.site-list-tools { @apply border-b border-gray-100 p-4 dark:border-dark-700; }
.site-list-items { @apply flex gap-2 overflow-x-auto p-2 xl:block xl:max-h-[900px] xl:overflow-y-auto; }
.site-item { @apply flex w-56 shrink-0 items-center gap-3 rounded-xl border border-transparent p-3 text-left transition-colors hover:bg-gray-50 dark:hover:bg-dark-800 xl:mb-1 xl:w-full; }
.site-item.selected { @apply border-primary-200 bg-primary-50/70 dark:border-primary-800 dark:bg-primary-950/30; }
.site-item strong { @apply text-sm font-medium; }
.site-avatar { @apply flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-primary-100 bg-primary-50 text-sm font-semibold text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-400; }
.site-avatar.large { @apply h-12 w-12 text-xl; }
.site-dot { @apply inline-block h-1.5 w-1.5 shrink-0 rounded-full bg-gray-400; }
.site-dot.ok { @apply bg-primary-500; }
.site-dot.attention { @apply bg-amber-500; }
.site-badge { @apply inline-flex items-center gap-1.5 whitespace-nowrap rounded-md bg-gray-100 px-2 py-1 text-[11px] text-gray-500 dark:bg-dark-700 dark:text-gray-400; }
.site-badge.ok { @apply bg-primary-50 text-primary-700 dark:bg-primary-950/40 dark:text-primary-400; }
.site-badge.attention { @apply bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-400; }
.site-detail { @apply overflow-hidden; }
.site-detail-heading { @apply flex flex-wrap items-center justify-between gap-4 p-5; }
.site-detail-heading h2 { @apply text-lg font-semibold; }
.site-metrics { @apply mx-5 mb-5 grid grid-cols-2 gap-4 rounded-xl border border-gray-100 bg-gray-50/70 px-5 py-5 dark:border-dark-700 dark:bg-dark-800/50 sm:grid-cols-[1.5fr_1fr_1fr]; }
.site-metrics > div { @apply min-w-0; }
.site-metrics > div:first-child { @apply col-span-2 sm:col-span-1; }
.site-metrics > div + div { @apply border-t border-gray-200 pt-4 dark:border-dark-600 sm:border-l sm:border-t-0 sm:pl-5 sm:pt-0; }
.site-metrics > div:nth-child(3) { @apply border-l pl-4; }
.site-metrics span { @apply block text-xs text-gray-500 dark:text-gray-400; }
.site-metrics strong { @apply my-2 block break-words text-2xl font-semibold tabular-nums; }
.site-metrics small { @apply text-[11px] font-normal text-gray-400; }
.site-metrics > div > small { @apply block; }
.console-tabs { @apply flex overflow-x-auto border-b border-gray-200 px-5 dark:border-dark-700; }
.console-tabs button { @apply flex shrink-0 items-center gap-2 border-b-2 border-transparent px-4 py-3 text-sm text-gray-500 first:pl-0 dark:text-gray-400; }
.console-tabs button.active { @apply border-primary-600 font-medium text-primary-700 dark:text-primary-400; }
.console-tabs span { @apply rounded bg-gray-100 px-1.5 text-[10px] text-gray-500 dark:bg-dark-700; }
.key-toolbar { @apply flex flex-wrap gap-2 p-5 pb-3; }
.key-toolbar .input { @apply w-auto min-w-0 flex-1 text-xs sm:max-w-[160px]; }
.key-toolbar .key-search { @apply basis-full sm:max-w-none sm:basis-auto; }
.key-batchbar { @apply flex flex-wrap items-center justify-between gap-3 px-5 pb-4; }
.key-batchbar p { @apply text-xs text-gray-500; }
.console-table-scroll { @apply w-full min-w-0 overflow-x-auto; }
.console-table { @apply w-full min-w-[850px] text-left text-xs; }
.console-table th { @apply whitespace-nowrap border-y border-gray-100 bg-gray-50/70 px-4 py-3 font-normal text-gray-500 dark:border-dark-700 dark:bg-dark-800/50; }
.console-table td { @apply border-b border-gray-100 px-4 py-4 align-middle dark:border-dark-700; }
.console-table th:first-child, .console-table td:first-child { @apply pl-5 pr-1; }
.console-table strong { @apply block max-w-44 break-words font-medium; }
.table-muted { @apply mt-1 max-w-44 break-words text-[10px] text-gray-400; }
.model-chip { @apply mb-1 mr-1 block max-w-44 truncate rounded bg-gray-100 px-1.5 py-1 text-[10px] text-gray-600 dark:bg-dark-800 dark:text-gray-300; }
.console-pagination { @apply flex flex-wrap items-center justify-between gap-3 px-5 py-4 text-xs text-gray-400; }
.account-card { @apply mb-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700; }
.model-preview { @apply mt-4 rounded-lg bg-gray-50 p-4 dark:bg-dark-800; }
.console-empty { @apply flex flex-col items-center gap-4 px-5 py-16 text-center text-gray-500; }
.console-empty h2 { @apply text-lg font-medium text-gray-700 dark:text-gray-200; }
.console-empty p { @apply max-w-md text-sm; }
button:disabled { @apply cursor-not-allowed opacity-50; }
</style>
