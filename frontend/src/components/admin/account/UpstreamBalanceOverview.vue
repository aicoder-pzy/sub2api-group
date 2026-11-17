<template>
  <section v-if="sites.length" class="mb-5 space-y-4" :aria-label="tr('balanceOverview')">
    <div class="grid gap-4 md:grid-cols-3">
      <button v-for="category in categories" :key="category.value" type="button" class="balance-card" :class="{ selected: purpose === category.value }" :aria-pressed="purpose === category.value" :data-balance-purpose="category.value || 'unclassified'" @click="purpose = category.value">
        <span class="text-sm text-gray-500">{{ tr(category.label) }}</span>
        <strong v-for="total in totals(category.value)" :key="total.currency" class="mt-2 block break-words text-2xl font-semibold tabular-nums">{{ money(total.value, total.currency) }}</strong>
        <strong v-if="!totals(category.value).length" class="mt-2 block text-2xl">{{ tr('unknownBalance') }}</strong>
        <span class="mt-3 block text-xs text-gray-500">{{ t('upstreamSites.balanceCoverage', { known: categorySites(category.value).filter(usable).length, total: categorySites(category.value).length }) }}</span>
        <span v-if="excluded(category.value)" class="mt-1 block text-xs text-amber-700 dark:text-amber-400">{{ t('upstreamSites.balanceExcluded', { count: excluded(category.value) }) }}</span>
      </button>
    </div>
    <div class="rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900 sm:p-5">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-sm font-medium">{{ tr('siteBalances') }} · {{ tr(categories.find(item => item.value === purpose)!.label) }}</h2>
        <div class="flex flex-wrap gap-2">
          <select v-model="currency" class="input !w-auto !py-1.5 !text-xs" :aria-label="tr('balanceCurrency')"><option v-for="unit in currencies" :key="unit" :value="unit">{{ unit }}</option></select>
          <select v-model="sort" class="input !w-auto !py-1.5 !text-xs" :aria-label="tr('balanceSort')"><option value="desc">{{ tr('balanceDescending') }}</option><option value="asc">{{ tr('balanceAscending') }}</option></select>
        </div>
      </div>
      <p class="mb-4 text-xs leading-5 text-gray-500">{{ tr('balanceHint') }}</p>
      <div class="max-h-72 space-y-2 overflow-y-auto" :aria-label="tr('siteBalances')">
        <button v-for="site in rows" :key="site.id" type="button" class="balance-row" :data-balance-site="site.id" @click="emit('select', site.id)">
          <span class="flex min-w-0 flex-wrap items-center justify-between gap-2"><span class="truncate text-sm font-medium">{{ site.name }}</span><strong class="text-sm tabular-nums" :class="{ 'text-amber-700 dark:text-amber-400': !usable(site), 'text-red-600': (site.inventory?.wallet?.remaining || 0) < 0 }">{{ display(site) }}</strong></span>
          <span class="my-2 block h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"><span v-if="finiteWallet(site)" class="block h-full rounded-full" :class="[usable(site) ? purpose === 'private' ? 'bg-violet-500' : 'bg-blue-500' : 'bg-amber-400', { 'bg-red-500': site.inventory!.wallet!.remaining! < 0 }]" :style="{ width: Math.max(0, Math.abs(site.inventory!.wallet!.remaining!) / maximum * 100) + '%' }"></span></span>
          <span class="block text-[11px] text-gray-500">{{ site.inventory?.errors.wallet ? tr('stale') + ' · ' : '' }}{{ tr('updated') }}：{{ time(site.inventory?.wallet_at) }}</span>
        </button>
        <p v-if="!rows.length" class="py-6 text-center text-sm text-gray-500">{{ tr('noBalanceSites') }}</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UpstreamSite, SitePurpose } from '@/api/admin/upstreamSites'

const props = defineProps<{ sites: UpstreamSite[] }>()
const emit = defineEmits<{ select: [id: string] }>()
const { t, locale } = useI18n()
const tr = (key: string) => t(`upstreamSites.${key}`)
const categories = [{ value: 'private', label: 'privateBalance' }, { value: 'public', label: 'publicBalance' }, { value: '', label: 'unclassifiedBalance' }] as const
const purpose = ref<SitePurpose>('private'), currency = ref(''), sort = ref('desc')
const categorySites = (value: SitePurpose) => props.sites.filter(site => (site.purpose || '') === value)
function finiteWallet(site: UpstreamSite) {
  const wallet = site.inventory?.wallet
  return !!wallet?.currency && !wallet.unlimited && typeof wallet.remaining === 'number' && Number.isFinite(wallet.remaining)
}
const usable = (site: UpstreamSite) => finiteWallet(site) && !site.inventory?.errors.wallet
const excluded = (value: SitePurpose) => categorySites(value).filter(site => !usable(site)).length
function totals(value: SitePurpose) {
  const sums = new Map<string, number>()
  for (const site of categorySites(value).filter(usable)) {
    const wallet = site.inventory!.wallet!
    sums.set(wallet.currency, (sums.get(wallet.currency) || 0) + wallet.remaining!)
  }
  return [...sums].sort(([a], [b]) => a.localeCompare(b)).map(([unit, total]) => ({ currency: unit, value: total }))
}
const currencies = computed(() => [...new Set(categorySites(purpose.value).map(site => site.inventory?.wallet?.currency).filter((unit): unit is string => !!unit))].sort())
watch(currencies, units => { if (!units.includes(currency.value)) currency.value = units[0] || '' }, { immediate: true })
const rows = computed(() => categorySites(purpose.value).filter(site => !site.inventory?.wallet?.currency || site.inventory.wallet.currency === currency.value).sort((a, b) => {
  if (finiteWallet(a) !== finiteWallet(b)) return finiteWallet(a) ? -1 : 1
  return ((a.inventory?.wallet?.remaining || 0) - (b.inventory?.wallet?.remaining || 0)) * (sort.value === 'asc' ? 1 : -1)
}))
const maximum = computed(() => Math.max(1, ...rows.value.filter(finiteWallet).map(site => Math.abs(site.inventory!.wallet!.remaining!))))
const money = (value: number, unit: string) => `${unit === 'QUOTA' ? tr('quotaUnit') : unit} ${value.toLocaleString(locale.value, { minimumFractionDigits: unit === 'QUOTA' ? 0 : 2, maximumFractionDigits: unit === 'QUOTA' ? 0 : 2 })}`
function display(site: UpstreamSite) {
  const wallet = site.inventory?.wallet
  if (wallet?.unlimited) return tr('unlimited')
  return finiteWallet(site) ? money(wallet!.remaining!, wallet!.currency) : tr(site.inventory?.errors.wallet ? 'balanceFailed' : 'unknownBalance')
}
const time = (value?: string) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US') : tr('never')
</script>

<style scoped>
.balance-card { @apply min-w-0 rounded-2xl border border-gray-200 bg-white p-5 text-left transition-colors hover:border-primary-400 dark:border-dark-700 dark:bg-dark-900; }
.balance-card.selected { @apply border-primary-500 ring-1 ring-primary-500; }
.balance-row { @apply block w-full rounded-xl p-3 text-left hover:bg-gray-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:hover:bg-dark-800; }
</style>
