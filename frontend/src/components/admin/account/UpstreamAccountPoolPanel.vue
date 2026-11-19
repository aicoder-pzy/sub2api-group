<template>
  <div class="space-y-4 px-1 py-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-gray-500" role="status">
        {{ snapshot?.updated ? t('accountPoolSnapshot', { count: snapshot.rows.length, time: new Date(snapshot.updated).toLocaleString() }) : t('accountPoolEmpty') }}
      </p>
      <button class="btn btn-secondary" type="button" :disabled="busy" @click="load(true)">
        <Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" />{{ t(refreshing ? 'accountPoolRefreshing' : 'accountPoolRefresh') }}
      </button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <details v-if="snapshot?.pools.length" class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
      <summary class="cursor-pointer text-sm font-medium">{{ t('accountPoolManage') }}</summary>
      <ul class="mt-2 divide-y divide-gray-200 dark:divide-dark-700">
        <li v-for="pool in snapshot.pools" :key="pool.id" class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 py-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
          <span class="col-span-2 min-w-0 break-words text-sm sm:col-span-1">{{ pool.original || pool.name }} <span class="text-gray-500">({{ pool.count }})</span></span>
          <input v-model="drafts[pool.id]" class="input min-w-0" maxlength="60" :aria-label="t('accountPoolName') + ' · ' + pool.name" :placeholder="pool.name" :disabled="busy" @keydown.enter.prevent="rename(pool)" />
          <button class="btn btn-secondary whitespace-nowrap" type="button" :disabled="busy || (drafts[pool.id] || '').trim() === (pool.alias || '')" @click="rename(pool)">{{ t('accountPoolSave') }}</button>
        </li>
      </ul>
    </details>
    <template v-if="snapshot?.updated">
      <div class="flex flex-wrap gap-2">
        <select v-model="selectedPool" class="input w-auto max-w-full" :aria-label="t('accountPool')">
          <option value="">{{ t('accountPoolAll') }}</option>
          <option v-for="pool in snapshot.pools" :key="pool.id" :value="pool.id">{{ pool.name }} ({{ pool.count }})</option>
        </select>
        <select v-model="selectedPlatform" class="input w-auto max-w-full" :aria-label="t('accountPoolPlatform')">
          <option value="">{{ t('accountPoolAllPlatforms') }}</option>
          <option v-for="platform in platforms" :key="platform" :value="platform">{{ platformLabel(platform) }}</option>
        </select>
      </div>
      <div v-if="shown.length" class="overflow-x-auto">
        <table class="w-full whitespace-nowrap text-left text-sm">
          <thead class="bg-gray-50 text-gray-600 dark:bg-dark-800 dark:text-gray-300">
            <tr><th v-for="label in ['accountPoolNumber', 'accountPoolPlatform', 'accountPoolPlan', 'accountPoolStatus', 'accountPoolConcurrency', 'accountPoolUsage', 'accountPoolLastUsed']" :key="label" class="px-3 py-2" scope="col">{{ t(label) }}</th></tr>
          </thead>
          <tbody class="divide-y divide-gray-200 dark:divide-dark-700">
            <tr v-for="row in shown" :key="row.number">
              <td class="px-3 py-2 tabular-nums">#{{ row.number }}</td>
              <td class="px-3 py-2">{{ platformLabel(row.platform) }}</td>
              <td class="px-3 py-2">{{ row.plan === 'unknown' ? '—' : row.plan }}</td>
              <td class="px-3 py-2" :class="row.status === 'active' ? 'text-emerald-700 dark:text-emerald-400' : row.status === 'rate_limited' ? 'text-amber-700 dark:text-amber-400' : ''">{{ t(`accountPoolState_${row.status}`) }}</td>
              <td class="px-3 py-2 tabular-nums">{{ row.limit == null ? '—' : `${row.current ?? '—'} / ${row.limit}` }}</td>
              <td class="px-3 py-2 tabular-nums">{{ row.windows?.map(w => `${w.name} ${percent(w.used)}`).join(' · ') || '—' }}</td>
              <td class="px-3 py-2 tabular-nums">{{ row.last_used ? new Date(row.last_used).toLocaleString() : '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else class="text-sm text-gray-500">{{ t('accountPoolNoResults') }}</p>
      <div v-if="pages > 1" class="flex flex-wrap items-center justify-end gap-2 text-sm">
        <button class="btn btn-secondary" type="button" :disabled="page === 1" :aria-label="t('accountPoolPrevious')" :title="t('accountPoolPrevious')" @click="page--">‹</button>
        <span class="px-1 tabular-nums" aria-live="polite">{{ page }} / {{ pages }}</span>
        <button class="btn btn-secondary" type="button" :disabled="page === pages" :aria-label="t('accountPoolNext')" :title="t('accountPoolNext')" @click="page++">›</button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { upstreamSitesAPI, siteError, type SitePool, type SitePoolDisplay } from '@/api/admin/upstreamSites'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ siteId: string }>()
const { t: translate } = useI18n()
const t = (key: string, params: Record<string, string | number> = {}) => translate(`upstreamSites.${key}`, params)
const snapshot = ref<SitePoolDisplay>(), drafts = ref<Record<string, string>>({})
const busy = ref(false), refreshing = ref(false), error = ref('')
const selectedPool = ref(''), selectedPlatform = ref(''), page = ref(1)
const size = 30
const platforms = computed(() => [...new Set(snapshot.value?.rows.map(row => row.platform))].sort())
const filtered = computed(() => (snapshot.value?.rows || []).filter(row => (!selectedPool.value || row.pools?.includes(selectedPool.value)) && (!selectedPlatform.value || row.platform === selectedPlatform.value)))
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / size)))
const shown = computed(() => filtered.value.slice((page.value - 1) * size, page.value * size))
const platformLabel = (id: string) => ({ openai: 'OpenAI', grok: 'Grok', anthropic: 'Anthropic', gemini: 'Gemini' }[id] || t('accountPoolState_unknown'))
const formatPercent = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1, style: 'percent' })
const percent = (value: number) => formatPercent.format(value / 100)
watch([selectedPool, selectedPlatform], () => { page.value = 1 })
watch(pages, count => { page.value = Math.min(page.value, count) })

async function load(refresh: boolean) {
  if (busy.value) return
  busy.value = true; refreshing.value = refresh; error.value = ''
  try {
    const next = await (refresh ? upstreamSitesAPI.refreshAccountPool(props.siteId) : upstreamSitesAPI.accountPool(props.siteId))
    snapshot.value = next
    if (!next.pools.some(pool => pool.id === selectedPool.value)) selectedPool.value = ''
    if (!platforms.value.includes(selectedPlatform.value)) selectedPlatform.value = ''
    drafts.value = Object.fromEntries(next.pools.map(pool => [pool.id, pool.alias || '']))
  } catch (cause) { error.value = siteError(cause) }
  finally { busy.value = false; refreshing.value = false }
}

async function rename(pool: SitePool) {
  const name = (drafts.value[pool.id] || '').trim()
  if (busy.value || name === (pool.alias || '')) return
  busy.value = true; error.value = ''
  try {
    snapshot.value = await upstreamSitesAPI.renameAccountPool(props.siteId, pool.id, name)
    drafts.value[pool.id] = name
  } catch (cause) { error.value = siteError(cause) }
  finally { busy.value = false }
}

onMounted(() => load(false))
</script>
