<template>
  <div class="space-y-4 px-1 py-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-gray-500" role="status">
        {{ updated ? tr('accountPoolSnapshot', { count: total, time: formatDateTime(updated) }) : tr(busy ? 'accountPoolLoading' : 'accountPoolEmpty') }}
      </p>
      <div class="flex flex-wrap items-center gap-2">
        <span v-if="refreshing" class="text-sm text-gray-500" role="status">{{ tr('accountPoolRefreshing') }}</span>
        <button class="btn btn-secondary" type="button" :disabled="groupMode ? busy : requestingRefresh || refreshing" @click="groupMode ? load() : requestRefresh()">
          <Icon name="refresh" size="sm" />{{ tr(groupMode ? 'accountPoolReload' : 'accountPoolRefresh') }}
        </button>
      </div>
    </div>
    <p v-if="stale" class="text-sm text-amber-700 dark:text-amber-300" role="status">{{ tr('accountPoolStale') }}</p>
    <p v-if="error || actionError || snapshot?.refresh_error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error || actionError || snapshot?.refresh_error }}</p>
    <p v-if="groupSnapshot && !groupSnapshot.complete" role="status" class="rounded-lg border border-amber-200 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:text-amber-300">{{ tr('accountPoolPartial') }}</p>
    <ul v-if="groupSnapshot?.plans.length" class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-700 dark:text-gray-300" :aria-label="tr('accountPoolPlans')">
      <li v-for="plan in groupSnapshot.plans" :key="plan.code">{{ plan.name }} <span class="tabular-nums text-gray-500 dark:text-gray-400">{{ plan.count }}</span></li>
    </ul>
    <details v-if="snapshot?.pools.length" class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
      <summary class="cursor-pointer text-sm font-medium">{{ tr('accountPoolManage') }}</summary>
      <ul class="mt-2 divide-y divide-gray-200 dark:divide-dark-700">
        <li v-for="pool in snapshot.pools" :key="pool.id" class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 py-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
          <span class="col-span-2 min-w-0 break-words text-sm sm:col-span-1">{{ pool.original || pool.name }} <span class="text-gray-500">({{ pool.count }})</span></span>
          <input v-model="drafts[pool.id]" class="input min-w-0" maxlength="60" :aria-label="tr('accountPoolName') + ' · ' + pool.name" :placeholder="pool.name" :disabled="savingAlias" @keydown.enter.prevent="rename(pool)" />
          <button class="btn btn-secondary whitespace-nowrap" type="button" :disabled="savingAlias || (drafts[pool.id] || '').trim() === (pool.alias || '')" @click="rename(pool)">{{ tr('accountPoolSave') }}</button>
        </li>
      </ul>
    </details>
    <RouterLink v-if="siteId" to="/admin/account-pools" class="text-sm text-primary-600 dark:text-primary-400">{{ tr('accountPoolManagement') }}</RouterLink>
    <template v-if="groupSnapshot || snapshot?.updated">
      <div v-if="snapshot && !groupMode" class="flex flex-wrap gap-2">
        <select v-model="selectedPool" class="input w-auto max-w-full" :aria-label="tr('accountPoolSourceGroup')">
          <option value="">{{ tr('accountPoolAll') }}</option>
          <option v-for="pool in snapshot.pools" :key="pool.id" :value="pool.id">
            {{ pool.original || pool.name }}<template v-if="pool.original && pool.alias && pool.alias !== pool.original"> · {{ pool.alias }}</template> ({{ pool.count }})
          </option>
        </select>
        <select v-model="selectedPlatform" class="input w-auto max-w-full" :aria-label="tr('accountPoolPlatform')">
          <option value="">{{ tr('accountPoolAllPlatforms') }}</option>
          <option v-for="platform in platforms" :key="platform" :value="platform">{{ platformLabel(platform) }}</option>
        </select>
      </div>
      <DataTable v-if="shown.length" :columns="columns" :data="shown" row-key="number">
        <template #cell-number="{ row, value }">
          <span v-if="row.admin?.name" class="text-sm font-medium" :title="row.admin.source_id ? `#${row.admin.source_id}` : undefined">{{ row.admin.name }}</span>
          <span v-else class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ row.admin?.source_id || value }}</span>
        </template>
        <template #cell-platform_type="{ row }">
          <PlatformTypeBadge
            v-if="row.platform !== 'unknown'"
            :platform="row.platform"
            :type="accountType(row)"
            :plan-type="row.plan === 'unknown' ? undefined : row.plan"
            :display-plan-label="row.admin?.plan || (row.plan === 'unknown' ? undefined : row.plan)"
          />
          <span v-else class="badge badge-gray text-xs">{{ tr('accountPoolState_unknown') }}</span>
        </template>
        <template #cell-status="{ row }">
          <span :class="['badge text-xs', statusBadge[row.status] || 'badge-gray']" :title="statusDetail(row)">{{ row.status === 'unknown' && row.admin?.status ? row.admin.status : tr(`accountPoolState_${row.status}`) }}</span>
        </template>
        <template #cell-rates="{ row }">
          <span v-if="row.admin?.account_rate != null" class="badge badge-gray tabular-nums">{{ row.admin.account_rate.toFixed(2) }}x</span>
          <div v-else class="flex flex-col items-start gap-1">
            <span v-for="pool in memberships(row)" :key="pool.id" class="badge badge-gray tabular-nums" :title="pool.original || pool.name">{{ row.admin?.rates?.[pool.id] != null ? `${row.admin.rates[pool.id].toFixed(2)}x` : '—' }}</span>
            <span v-if="!memberships(row).length" class="text-gray-400">—</span>
          </div>
        </template>
        <template #cell-groups="{ row }">
          <div class="flex flex-col items-start gap-1">
            <span v-for="pool in memberships(row)" :key="pool.id" class="badge badge-gray">{{ pool.original || pool.name }}</span>
            <span v-if="!row.pools?.length" class="text-gray-400">—</span>
          </div>
        </template>
        <template #cell-capacity="{ row }">
          <AccountCapacityCell v-if="row.current != null && row.limit != null" class="!inline-flex" :title="capacityTitle(row)" :account="{ platform: row.platform, type: accountType(row), current_concurrency: row.current, concurrency: row.limit }" />
          <span v-else class="font-mono text-xs text-gray-500">{{ row.limit == null ? '—' : `— / ${row.limit}` }}</span>
        </template>
        <template #cell-usage="{ row }">
          <div v-if="row.windows?.length" class="inline-flex flex-col gap-1">
            <UsageProgressBar v-for="(usageWindow, index) in row.windows" :key="index" :label="usageWindow.name" :utilization="usageWindow.used" :resets-at="usageWindow.name === '5h' ? row.admin?.reset_5h : usageWindow.name === '7d' ? row.admin?.reset_7d : undefined" :color="usageWindow.name === '5h' ? 'indigo' : 'emerald'" :label-width="usageWindow.name.length > 2 ? 'auto' : 'fixed'" />
          </div>
          <span v-else class="text-gray-400">—</span>
        </template>
        <template #cell-last_used="{ row }">
          <span class="text-sm text-gray-500 dark:text-dark-400" :title="activityTitle(row)">{{ activity(row) ? formatRelativeTime(activity(row), now) : '—' }}</span>
        </template>
      </DataTable>
      <p v-else class="text-sm text-gray-500">{{ tr('accountPoolNoResults') }}</p>
      <div v-if="pages > 1" class="flex flex-wrap items-center justify-end gap-2 text-sm">
        <button class="btn btn-secondary" type="button" :disabled="(groupMode && busy) || page === 1" :aria-label="tr('accountPoolPrevious')" :title="tr('accountPoolPrevious')" @click="page--">‹</button>
        <span class="px-1 tabular-nums" aria-live="polite">{{ page }} / {{ pages }}</span>
        <button class="btn btn-secondary" type="button" :disabled="(groupMode && busy) || page >= pages" :aria-label="tr('accountPoolNext')" :title="tr('accountPoolNext')" @click="page++">›</button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useIntervalFn } from '@vueuse/core'
import { upstreamSitesAPI, siteError, type GroupPoolDisplay, type SitePool, type SitePoolDisplay } from '@/api/admin/upstreamSites'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'
import DataTable from '@/components/common/DataTable.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import type { AccountType } from '@/types'

const props = defineProps<{ siteId?: string; groupId?: number }>()
const { t: translate } = useI18n()
const tr = (key: string, params: Record<string, string | number> = {}) => translate(`upstreamSites.${key}`, params)
const snapshot = shallowRef<SitePoolDisplay>(), drafts = ref<Record<string, string>>({})
const groupSnapshot = ref<GroupPoolDisplay>()
const groupMode = computed(() => props.groupId != null)
const updated = computed(() => groupSnapshot.value?.updated ?? snapshot.value?.updated)
const total = computed(() => groupSnapshot.value?.total ?? snapshot.value?.page_info?.total ?? 0)
const busy = ref(false), refreshing = ref(false), error = ref('')
const actionError = ref(''), requestingRefresh = ref(false), savingAlias = ref(false)
const now = ref(Date.now())
useIntervalFn(() => { now.value = Date.now() }, 60_000)
const isOld = (stamp: string | null | undefined) => !stamp || now.value - Date.parse(stamp) >= 15 * 60_000
const stale = computed(() => !groupMode.value && !!updated.value && isOld(updated.value))
const selectedPool = ref(''), selectedPlatform = ref(''), page = ref(1)
const size = 30
const platforms = computed(() => snapshot.value?.page_info?.platforms ?? [])
const siteQuery = () => ({ page: page.value, page_size: size, pool_id: selectedPool.value || undefined, platform: selectedPlatform.value || undefined })
type PoolRow = SitePoolDisplay['rows'][number]
const capacityTitle = (row: PoolRow) => {
  const stamp = row.admin?.observed_at || updated.value
  return stamp ? `${tr('accountPoolObservedAt')} · ${formatDateTime(stamp)}` : undefined
}
const concurrencyActivity = (row: PoolRow) => {
  const observed = row.admin?.observed_at
  return (row.current ?? 0) > 0 && observed && Date.parse(observed) > (Date.parse(row.last_used || '') || 0) ? observed : undefined
}
const activity = (row: PoolRow) => row.admin?.usage_updated || concurrencyActivity(row) || row.last_used
const activityTitle = (row: PoolRow) => {
  const stamp = activity(row)
  if (!stamp) return undefined
  if (!row.admin?.usage_updated && concurrencyActivity(row)) {
    const used = row.last_used ? ` · ${tr('accountPoolLastUsed')} ${formatDateTime(row.last_used)}` : ''
    return `${tr('accountPoolObservedAt')} · ${formatDateTime(stamp)}${used}`
  }
  return `${tr(row.admin?.usage_updated ? 'accountPoolUsageUpdated' : 'accountPoolLastUsed')} · ${formatDateTime(stamp)}`
}
const accountType = (row: PoolRow): AccountType => (row.admin?.type || 'oauth') as AccountType
const statusDetail = (row: PoolRow) => [row.admin?.status, row.admin?.intelligence,
  row.admin?.priority != null ? tr('accountPoolPriority', { value: row.admin.priority }) : '',
  row.admin?.recover_at ? tr('accountPoolRecoverAt', { time: formatDateTime(row.admin.recover_at) }) : ''].filter(Boolean).join(' · ') || undefined
const poolById = computed(() => new Map(snapshot.value?.pools.map(pool => [pool.id, pool])))
const memberships = (row: PoolRow) => (row.pools || []).flatMap(id => {
  const pool = poolById.value.get(id)
  return pool ? [pool] : []
})
const pages = computed(() => groupSnapshot.value?.pages ?? snapshot.value?.page_info?.pages ?? 1)
const shown = computed(() => groupSnapshot.value?.rows ?? snapshot.value?.rows ?? [])
const platformLabel = (id: string) => ({ openai: 'OpenAI', grok: 'Grok', anthropic: 'Anthropic', gemini: 'Gemini' }[id] || tr('accountPoolState_unknown'))
const columns = computed(() => [
  { key: 'number', label: tr('accountPoolNumber') },
  { key: 'platform_type', label: translate('admin.accounts.columns.platformType') },
  ...(!groupMode.value ? [{ key: 'rates', label: tr('accountPoolEffectiveRate') }] : [{ key: 'status', label: tr('accountPoolStatus') }]),
  { key: 'capacity', label: tr('accountPoolConcurrency') },
  ...(!groupMode.value ? [{ key: 'status', label: tr('accountPoolStatus') }, { key: 'groups', label: tr('accountPoolSourceGroup') }] : []),
  { key: 'usage', label: translate('admin.accounts.columns.usageWindows') },
  { key: 'last_used', label: tr(groupMode.value ? 'accountPoolLastUsed' : 'accountPoolLastActivity') },
])
const statusBadge: Record<string, string> = { active: 'badge-success', rate_limited: 'badge-warning', temp_unschedulable: 'badge-warning', overload: 'badge-danger', error: 'badge-danger' }
watch([selectedPool, selectedPlatform], () => { if (page.value === 1) void load(); else page.value = 1 })
watch(pages, count => { page.value = Math.min(page.value, count) })
watch(page, () => { void load() })

let request = 0
let reading = false, unmounted = false
useIntervalFn(() => { if (!groupMode.value) void load(true) }, computed(() => refreshing.value ? 10_000 : 30_000))

async function load(quiet = false) {
  if (unmounted || (quiet && (reading || savingAlias.value))) return
  const id = ++request
  reading = true
  if (!quiet) busy.value = true
  try {
    if (props.groupId != null) {
      const next = await upstreamSitesAPI.groupAccountPool(props.groupId, page.value, size)
      if (id !== request) return
      groupSnapshot.value = next
    } else if (props.siteId) {
      const next = await upstreamSitesAPI.accountPool(props.siteId, siteQuery())
      if (id !== request) return
      now.value = Date.now()
      const previous = poolById.value
      for (const pool of next.pools) {
        if ((drafts.value[pool.id] || '') === (previous.get(pool.id)?.alias || '')) drafts.value[pool.id] = pool.alias || ''
      }
      snapshot.value = next
      refreshing.value = !!next.refreshing
    }
    error.value = ''
  } catch (cause) { if (id === request) error.value = siteError(cause) }
  finally { if (id === request) { reading = false; busy.value = false } }
}

async function requestRefresh() {
  if (!props.siteId || requestingRefresh.value || refreshing.value) return
  requestingRefresh.value = true; actionError.value = ''
  try {
    await upstreamSitesAPI.refreshAccountPool(props.siteId)
    refreshing.value = true
    void load(true)
  } catch (cause) { actionError.value = siteError(cause) }
  finally { requestingRefresh.value = false }
}

async function rename(pool: SitePool) {
  const name = (drafts.value[pool.id] || '').trim()
  if (!props.siteId || savingAlias.value || name === (pool.alias || '')) return
  savingAlias.value = true; actionError.value = ''
  try {
    await upstreamSitesAPI.renameAccountPool(props.siteId, pool.id, name, siteQuery())
    drafts.value[pool.id] = name
    await load()
  } catch (cause) { actionError.value = siteError(cause) }
  finally { savingAlias.value = false }
}

onMounted(() => { void load() })
onUnmounted(() => { unmounted = true; request++ })
</script>
