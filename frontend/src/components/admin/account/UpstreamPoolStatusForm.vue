<template>
  <form class="mt-3 space-y-3 text-sm" @submit.prevent="save">
    <p v-if="error" role="alert" class="text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="saved" role="status">{{ tr('accountPoolPublishedSaved') }}</p>
    <code class="block break-all text-xs text-gray-500">GET /api/v1/integrations/account-pool-status</code>
    <fieldset class="space-y-3" :disabled="busy || !loaded">
      <label class="block max-w-md">
        <span>{{ tr('accountPoolSourceGroup') }}</span>
        <select v-model="form.pool_id" class="input mt-1" @change="changePool">
          <option value="">{{ tr('accountPoolUnpublished') }}</option>
          <option v-if="form.pool_id && !selected" :value="form.pool_id" disabled>{{ tr('accountPoolMissingGroup') }}</option>
          <option v-for="pool in snapshot.pools" :key="pool.id" :value="pool.id">{{ pool.original || pool.name }}</option>
        </select>
      </label>
      <template v-if="form.pool_id">
        <div class="grid gap-3 sm:grid-cols-2">
          <label>{{ tr('accountPoolSourceName') }}<input v-model.trim="form.source_name" class="input mt-1" required maxlength="60" /></label>
          <label>{{ tr('accountPoolPublicName') }}<input v-model.trim="form.name" class="input mt-1" required maxlength="60" /></label>
        </div>
        <div v-for="plan in originalPlans" :key="plan" class="grid grid-cols-2 items-center gap-3">
          <span class="min-w-0 break-words">{{ plan }}</span>
          <input :value="aliases[plan.toLowerCase()] || ''" class="input min-w-0" maxlength="60" :aria-label="tr('accountPoolPlanAlias', { plan })" :placeholder="defaultPlan(plan)" @change="setAlias(plan, ($event.target as HTMLInputElement).value)" />
        </div>
        <fieldset class="flex flex-wrap gap-x-4 gap-y-2">
          <legend class="mb-1">{{ tr('accountPoolAllowedPlans') }}</legend>
          <label v-for="plan in displayedPlans" :key="plan" class="inline-flex items-center gap-2 break-words">
            <input v-model="allowed" :value="plan" type="checkbox" /> {{ plan }}
          </label>
        </fieldset>
      </template>
      <button class="btn btn-primary" type="submit" :disabled="!!form.pool_id && (!selected || allowed.length === 0)">{{ tr('accountPoolSave') }}</button>
    </fieldset>
  </form>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { siteError, upstreamSitesAPI as api, type PoolStatusConfig, type SitePoolDisplay } from '@/api/admin/upstreamSites'

const props = defineProps<{ siteId: string; snapshot: SitePoolDisplay }>()
const { t } = useI18n()
const tr = (key: string, params: Record<string, string> = {}) => t(`upstreamSites.${key}`, params)
const form = ref<PoolStatusConfig>({ pool_id: '', source_name: '', name: '', plan_map: {}, plans: [] })
const aliases = ref<Record<string, string>>({})
const allowed = ref<string[]>([])
const busy = ref(false), loaded = ref(false), saved = ref(false), error = ref('')
const selected = computed(() => props.snapshot.pools.find(pool => pool.id === form.value.pool_id))
const originalPlans = computed(() => props.snapshot.page_info?.plans_by_pool[form.value.pool_id] ?? [])
const defaultPlan = (plan: string) => plan.toLowerCase() === 'promax' ? 'Pro Max' : plan
const displayPlan = (plan: string) => aliases.value[plan.toLowerCase()] || defaultPlan(plan)
const displayedPlans = computed(() => [...new Map([...originalPlans.value.map(displayPlan), ...allowed.value].map(plan => [plan.toLowerCase(), plan])).values()].sort())

function fill(next: PoolStatusConfig) {
  form.value = next
  aliases.value = { ...next.plan_map }
  allowed.value = next.plans || []
}

function changePool() {
  form.value.name = selected.value?.alias || selected.value?.original || selected.value?.name || ''
  aliases.value = {}
  allowed.value = []
  saved.value = false
}

function setAlias(plan: string, value: string) {
  const key = plan.toLowerCase(), before = displayPlan(plan)
  if (value.trim()) aliases.value[key] = value.trim()
  else delete aliases.value[key]
  const after = displayPlan(plan)
  if (before !== after && allowed.value.includes(before) && !originalPlans.value.some(item => displayPlan(item) === before)) {
    allowed.value = [...new Set(allowed.value.map(item => item === before ? after : item))]
  }
  saved.value = false
}

async function save() {
  busy.value = true; saved.value = false; error.value = ''
  try {
    fill(await api.savePoolStatusConfig(props.siteId, { ...form.value, plan_map: aliases.value, plans: allowed.value }))
    saved.value = true
  } catch (cause) { error.value = siteError(cause) }
  finally { busy.value = false }
}

onMounted(async () => {
  busy.value = true
  try { fill(await api.poolStatusConfig(props.siteId)); loaded.value = true }
  catch (cause) { error.value = siteError(cause) }
  finally { busy.value = false }
})
</script>
