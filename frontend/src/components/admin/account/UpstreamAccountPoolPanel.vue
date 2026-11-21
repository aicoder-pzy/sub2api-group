<template>
  <div class="space-y-4 px-1 py-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-gray-500" role="status">
        {{ updated ? tr('accountPoolSnapshot', { count: total, time: formatDateTime(updated) }) : tr(busy ? 'accountPoolLoading' : 'accountPoolEmpty') }}
      </p>
      <button class="btn btn-secondary" type="button" :disabled="busy || refreshing" @click="load(!groupMode)">
        <Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" />{{ tr(groupMode ? 'accountPoolReload' : refreshing ? 'accountPoolRefreshing' : 'accountPoolRefresh') }}
      </button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="groupSnapshot && !groupSnapshot.complete" role="status" class="rounded-lg border border-amber-200 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:text-amber-300">{{ tr('accountPoolPartial') }}</p>
    <ul v-if="groupSnapshot?.plans.length" class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-700 dark:text-gray-300" :aria-label="tr('accountPoolPlans')">
      <li v-for="plan in groupSnapshot.plans" :key="plan.code">{{ plan.name }} <span class="tabular-nums text-gray-500 dark:text-gray-400">{{ plan.count }}</span></li>
    </ul>
    <details v-if="snapshot?.pools.length" class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
      <summary class="cursor-pointer text-sm font-medium">{{ tr('accountPoolManage') }}</summary>
      <ul class="mt-2 divide-y divide-gray-200 dark:divide-dark-700">
        <li v-for="pool in snapshot.pools" :key="pool.id" class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 py-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
          <span class="col-span-2 min-w-0 break-words text-sm sm:col-span-1">{{ pool.original || pool.name }} <span class="text-gray-500">({{ pool.count }})</span></span>
          <input v-model="drafts[pool.id]" class="input min-w-0" maxlength="60" :aria-label="tr('accountPoolName') + ' · ' + pool.name" :placeholder="pool.name" :disabled="busy" @keydown.enter.prevent="rename(pool)" />
          <button class="btn btn-secondary whitespace-nowrap" type="button" :disabled="busy || (drafts[pool.id] || '').trim() === (pool.alias || '')" @click="rename(pool)">{{ tr('accountPoolSave') }}</button>
        </li>
      </ul>
    </details>
    <details v-if="siteId && snapshot" class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700" @toggle="editStatus = ($event.target as HTMLDetailsElement).open">
      <summary class="cursor-pointer text-sm font-medium">{{ tr('accountPoolPublish') }}</summary>
      <UpstreamPoolStatusForm v-if="editStatus" :site-id="siteId" :snapshot="snapshot" />
    </details>
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
        <template #cell-number="{ value }">
          <span class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ value }}</span>
        </template>
        <template #cell-platform_type="{ row }">
          <PlatformTypeBadge
            v-if="row.platform !== 'unknown'"
            :platform="row.platform"
            type="oauth"
            :plan-type="row.plan === 'unknown' ? undefined : row.plan"
            :display-plan-label="row.plan === 'unknown' ? undefined : row.plan"
          />
          <span v-else class="badge badge-gray text-xs">{{ tr('accountPoolState_unknown') }}</span>
        </template>
        <template #cell-status="{ row }">
          <span :class="['badge text-xs', statusBadge[row.status] || 'badge-gray']">{{ tr(`accountPoolState_${row.status}`) }}</span>
        </template>
        <template #cell-capacity="{ row }">
          <AccountCapacityCell v-if="row.current != null && row.limit != null" class="!inline-flex" :account="{ platform: row.platform, type: 'oauth', current_concurrency: row.current, concurrency: row.limit }" />
          <span v-else class="font-mono text-xs text-gray-500">{{ row.limit == null ? '—' : `— / ${row.limit}` }}</span>
        </template>
        <template #cell-usage="{ row }">
          <div v-if="row.windows?.length" class="inline-flex flex-col gap-1">
            <UsageProgressBar v-for="(usageWindow, index) in row.windows" :key="index" :label="usageWindow.name" :utilization="usageWindow.used" :color="usageWindow.name === '5h' ? 'indigo' : 'emerald'" :label-width="usageWindow.name.length > 2 ? 'auto' : 'fixed'" />
          </div>
          <span v-else class="text-gray-400">—</span>
        </template>
        <template #cell-last_used="{ value }">
          <span class="text-sm text-gray-500 dark:text-dark-400" :title="value ? formatDateTime(value) : undefined">{{ value ? formatRelativeTime(value) : '—' }}</span>
        </template>
      </DataTable>
      <p v-else class="text-sm text-gray-500">{{ tr('accountPoolNoResults') }}</p>
      <div v-if="pages > 1" class="flex flex-wrap items-center justify-end gap-2 text-sm">
        <button class="btn btn-secondary" type="button" :disabled="busy || page === 1" :aria-label="tr('accountPoolPrevious')" :title="tr('accountPoolPrevious')" @click="page--">‹</button>
        <span class="px-1 tabular-nums" aria-live="polite">{{ page }} / {{ pages }}</span>
        <button class="btn btn-secondary" type="button" :disabled="busy || page >= pages" :aria-label="tr('accountPoolNext')" :title="tr('accountPoolNext')" @click="page++">›</button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { upstreamSitesAPI, siteError, type GroupPoolDisplay, type SitePool, type SitePoolDisplay } from '@/api/admin/upstreamSites'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'
import DataTable from '@/components/common/DataTable.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import UpstreamPoolStatusForm from './UpstreamPoolStatusForm.vue'
import { formatDateTime, formatRelativeTime } from '@/utils/format'

const props = defineProps<{ siteId?: string; groupId?: number }>()
const { t: translate } = useI18n()
const tr = (key: string, params: Record<string, string | number> = {}) => translate(`upstreamSites.${key}`, params)
const snapshot = ref<SitePoolDisplay>(), drafts = ref<Record<string, string>>({})
const groupSnapshot = ref<GroupPoolDisplay>()
const editStatus = ref(false)
const groupMode = computed(() => props.groupId != null)
const updated = computed(() => groupSnapshot.value?.updated ?? snapshot.value?.updated)
const total = computed(() => groupSnapshot.value?.total ?? snapshot.value?.rows.length ?? 0)
const busy = ref(false), refreshing = ref(false), error = ref('')
const selectedPool = ref(''), selectedPlatform = ref(''), page = ref(1)
const size = 30
const platforms = computed(() => [...new Set(snapshot.value?.rows.map(row => row.platform))].sort())
const filtered = computed(() => (snapshot.value?.rows || []).filter(row => (!selectedPool.value || row.pools?.includes(selectedPool.value)) && (!selectedPlatform.value || row.platform === selectedPlatform.value)))
const pages = computed(() => groupSnapshot.value?.pages ?? Math.max(1, Math.ceil(filtered.value.length / size)))
const shown = computed(() => groupSnapshot.value?.rows ?? filtered.value.slice((page.value - 1) * size, page.value * size))
const platformLabel = (id: string) => ({ openai: 'OpenAI', grok: 'Grok', anthropic: 'Anthropic', gemini: 'Gemini' }[id] || tr('accountPoolState_unknown'))
const columns = computed(() => [
  { key: 'number', label: tr('accountPoolNumber') },
  { key: 'platform_type', label: translate('admin.accounts.columns.platformType') },
  { key: 'status', label: tr('accountPoolStatus') },
  { key: 'capacity', label: tr('accountPoolConcurrency') },
  { key: 'usage', label: translate('admin.accounts.columns.usageWindows') },
  { key: 'last_used', label: tr('accountPoolLastUsed') },
])
const statusBadge: Record<string, string> = { active: 'badge-success', rate_limited: 'badge-warning', temp_unschedulable: 'badge-warning', overload: 'badge-danger', error: 'badge-danger' }
watch([selectedPool, selectedPlatform], () => { page.value = 1 })
watch(pages, count => { page.value = Math.min(page.value, count) })
watch(page, () => { if (groupMode.value) void load(false) })

let timer: number | undefined
let unmounted = false
function poll() {
  if (refreshing.value && !unmounted) timer = window.setTimeout(() => { void load(false) }, 5000)
}

async function load(refresh: boolean) {
  if (busy.value || unmounted) { poll(); return }
  if (refresh) refreshing.value = true
  busy.value = true; error.value = ''
  try {
    if (props.groupId != null) {
      const next = await upstreamSitesAPI.groupAccountPool(props.groupId, page.value, size)
      groupSnapshot.value = next
      return
    }
    if (!props.siteId) return
    if (refresh) await upstreamSitesAPI.refreshAccountPool(props.siteId)
    const next = await upstreamSitesAPI.accountPool(props.siteId)
    const changed = snapshot.value?.updated !== next.updated
    snapshot.value = next
    refreshing.value = !!next.refreshing
    error.value = next.refresh_error || ''
    if (!next.pools.some(pool => pool.id === selectedPool.value)) selectedPool.value = ''
    if (!platforms.value.includes(selectedPlatform.value)) selectedPlatform.value = ''
    if (changed) drafts.value = Object.fromEntries(next.pools.map(pool => [pool.id, pool.alias || '']))
  } catch (cause) { error.value = siteError(cause); refreshing.value = false }
  finally { busy.value = false; poll() }
}

async function rename(pool: SitePool) {
  const name = (drafts.value[pool.id] || '').trim()
  if (!props.siteId || busy.value || name === (pool.alias || '')) return
  busy.value = true; error.value = ''
  try {
    snapshot.value = await upstreamSitesAPI.renameAccountPool(props.siteId, pool.id, name)
    drafts.value[pool.id] = name
  } catch (cause) { error.value = siteError(cause) }
  finally { busy.value = false }
}

onMounted(() => load(false))
onUnmounted(() => { unmounted = true; window.clearTimeout(timer) })
</script>
