<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3"><h1 class="text-xl font-semibold">{{ text('title') }}</h1><RouterLink to="/admin/channels" class="btn btn-secondary">{{ text('channels') }}</RouterLink></div>
      <form class="card space-y-3 p-5" @submit.prevent="applyFilters">
        <div class="flex flex-wrap gap-3">
          <label>{{ text('searchChannel') }}<input v-model="channelSearch" class="input block" maxlength="100" /></label>
          <button type="button" class="btn btn-secondary self-end" :disabled="loading" @click="channelPage = 1; refresh()">{{ text('apply') }}</button>
          <label>{{ text('channels') }}<select v-model.number="channelId" class="input block" @change="selectChannel"><option :value="0">{{ text('all') }}</option><option v-for="channel in channelOptions" :key="channel.id" :value="channel.id">{{ channel.name }} #{{ channel.id }}</option></select></label>
          <button type="button" class="btn btn-secondary self-end" :disabled="loading || channelPage <= 1" @click="channelPage--; refresh()">{{ text('previous') }}</button><button type="button" class="btn btn-secondary self-end" :disabled="loading || channelPage * pageSize >= channelTotal" @click="channelPage++; refresh()">{{ text('next') }}</button>
        </div>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <label>{{ text('from') }}<input v-model="from" type="datetime-local" class="input block w-full" required /></label>
          <label>{{ text('until') }}<input v-model="until" type="datetime-local" class="input block w-full" required /></label>
          <label>{{ text('group') }}<input v-model.number="groupId" type="number" min="0" class="input block w-full" /></label>
          <label>{{ text('model') }}<input v-model="model" maxlength="200" class="input block w-full" /></label>
          <label>{{ text('modelScope') }}<select v-model="modelScope" class="input block w-full"><option value="public">{{ text('publicModel') }}</option><option value="recorded">{{ text('recordedModel') }}</option><option value="upstream">{{ text('upstreamModel') }}</option></select></label>
        </div>
        <div class="flex flex-wrap gap-2"><button class="btn btn-primary" :disabled="loading || busy">{{ text('apply') }}</button><button type="button" class="btn btn-secondary" :disabled="loading || busy" @click="last24Hours">{{ text('last24') }}</button><button v-if="upstreamId || accountId" type="button" class="btn btn-secondary" @click="upstreamId = accountId = 0; applyFilters()">{{ text('clearDrill') }}</button><span class="self-center text-sm">{{ text('upstream') }} #{{ upstreamId || '—' }} · {{ text('account') }} #{{ accountId || '—' }}</span></div>
      </form>
      <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
      <p class="text-sm text-gray-500">{{ text('attributionNote') }}</p>
      <div v-if="snapshot" :aria-busy="loading" class="space-y-5" :class="{ 'opacity-60': loading }">
        <section class="card space-y-3 p-5">
          <h2 class="font-semibold">{{ text('latency') }} · {{ snapshot.from }} — {{ snapshot.until }}</h2>
          <p v-if="snapshot.usage.status !== 'ready'" role="alert">{{ text('queryFailed') }}</p>
          <template v-else-if="snapshot.usage.data">
            <p>{{ text('requests') }} {{ snapshot.usage.data.summary.requests }} · P50 {{ ms(snapshot.usage.data.summary.p50_ms) }} · P90 {{ ms(snapshot.usage.data.summary.p90_ms) }} · P95 {{ ms(snapshot.usage.data.summary.p95_ms) }} · {{ text('samples') }} {{ snapshot.usage.data.summary.samples }} / {{ snapshot.usage.data.summary.unknown_samples }}</p>
            <div class="overflow-x-auto"><table class="w-full min-w-[700px] text-left text-sm"><thead><tr><th>{{ text(dimension === 'channel' ? 'channels' : dimension === 'upstream' ? 'upstream' : 'account') }}</th><th>{{ text('requests') }}</th><th>P50 / P90 / P95</th><th>{{ text('samples') }}</th><th>{{ text('duration') }} P90</th><th>{{ text('procurementCost') }}</th></tr></thead><tbody><tr v-for="row in snapshot.usage.data.rows" :key="row.dimension_id" class="border-t dark:border-dark-700"><td><button class="text-primary-600 underline" :disabled="dimension === 'account' || loading" @click="drill(row.dimension_id)">{{ row.name }} #{{ row.dimension_id }}</button></td><td>{{ row.requests }}</td><td>{{ ms(row.p50_ms) }} / {{ ms(row.p90_ms) }} / {{ ms(row.p95_ms) }}</td><td>{{ row.samples }} / {{ row.unknown_samples }}</td><td>{{ ms(row.duration_p90_ms) }}</td><td>${{ row.account_cost.toFixed(4) }}</td></tr></tbody></table></div>
            <div class="flex gap-3"><button class="btn btn-secondary" :disabled="loading || statsPage <= 1" @click="statsPage--; refresh()">{{ text('previous') }}</button><span>{{ statsPage }} · {{ snapshot.usage.data.total }}</span><button class="btn btn-secondary" :disabled="loading || statsPage * pageSize >= snapshot.usage.data.total" @click="statsPage++; refresh()">{{ text('next') }}</button></div>
            <details><summary>{{ text('lineage') }} · {{ snapshot.usage.data.lineage.length }} / {{ snapshot.usage.data.lineage_total }}</summary><div v-for="(line,index) in snapshot.usage.data.lineage" :key="index" class="break-all py-1 text-sm">{{ line.public_model }} → {{ line.recorded_model }} → {{ line.upstream_model || '—' }} · {{ line.samples }}<p class="text-gray-500">{{ line.model_mapping_chain }}</p></div></details>
          </template>
        </section>
        <section class="card space-y-2 p-5"><h2 class="font-semibold">{{ text('quality') }}</h2><p class="text-sm text-gray-500">{{ text('currentScopeNote') }}</p><p v-if="snapshot.quality.status === 'unsupported_scope'">{{ text('qualityScope') }}</p><p v-else-if="snapshot.quality.status !== 'ready'" role="alert">{{ text('queryFailed') }}</p><template v-else-if="snapshot.quality.data"><p>{{ text('qualitySampleCounts', snapshot.quality.data.summary) }}</p><div v-for="row in snapshot.quality.data.rows" :key="row.id" class="text-sm">#{{ row.account_id }} · {{ row.model }} · {{ row.effort }} · {{ row.verdict }} · {{ new Date(row.sampled_at).toLocaleString() }}</div><button v-if="snapshot.quality.data.has_more" class="btn btn-secondary" :disabled="loading" @click="olderQuality">{{ text('older') }}</button></template></section>
        <section class="card space-y-2 p-5"><h2 class="font-semibold">{{ text('errors') }}</h2><p class="text-sm text-gray-500">{{ text('currentScopeNote') }} {{ text('errorNote') }}</p><p v-if="snapshot.errors.status !== 'ready'" role="alert">{{ text('queryFailed') }}</p><template v-else-if="snapshot.errors.data"><p>{{ text('errorRecords') }} {{ snapshot.errors.data.records }}</p><div v-for="(row,index) in snapshot.errors.data.rows" :key="index" class="text-sm">{{ row.error_phase }} · {{ row.error_type }} · HTTP {{ row.status_code || '—' }} · {{ row.records }}</div></template></section>
        <section class="card space-y-2 p-5"><h2 class="font-semibold">{{ text('capacity') }}</h2><p class="text-sm text-gray-500">{{ text('capacityNote') }}</p><p v-if="snapshot.capacity.status !== 'ready'" role="alert">{{ text('queryFailed') }}</p><p v-else-if="snapshot.capacity.data">{{ text('accounts') }} {{ snapshot.capacity.data.accounts }} · {{ text('schedulable') }} {{ snapshot.capacity.data.schedulable }} · {{ text('concurrency') }} {{ snapshot.capacity.data.configured_concurrency }}</p></section>
      </div>
      <section v-if="channelId" class="card space-y-4 p-5" :aria-busy="loading">
        <h2 class="font-semibold">{{ text('membership') }}</h2><p class="text-sm text-gray-500">{{ text('membershipNote') }}</p>
        <form class="flex flex-wrap gap-2" @submit.prevent="upstreamPage = 1; refresh()"><label>{{ text('searchUpstream') }}<input v-model="upstreamSearch" class="input block" maxlength="100" /></label><button class="btn btn-secondary self-end" :disabled="loading">{{ text('apply') }}</button><button type="button" class="btn btn-primary self-end" :disabled="busy || loading || !catalog" @click="editing = { id: 0, name: '' }">{{ text('createUpstream') }}</button></form>
        <template v-if="catalog">
          <div v-for="supplier in catalog.upstreams" :key="supplier.id" class="flex flex-wrap items-center gap-3"><button class="text-primary-600 underline" :disabled="loading" @click="upstreamId = supplier.id; accountId = 0; applyFilters()">{{ supplier.name }} #{{ supplier.id }} · {{ supplier.members }}</button><button class="btn btn-secondary" :disabled="busy || loading" @click="editing = { id: supplier.id, name: supplier.name }">{{ text('rename') }}</button><button class="btn btn-danger" :disabled="busy || loading" @click="queue({ action: 'delete_upstream', upstream_id: supplier.id, confirm: true })">{{ text('deleteUpstream') }}</button></div>
          <div class="flex gap-3"><button class="btn btn-secondary" :disabled="loading || upstreamPage <= 1" @click="upstreamPage--; refresh()">{{ text('previous') }}</button><span>{{ upstreamPage }} · {{ catalog.upstream_total }}</span><button class="btn btn-secondary" :disabled="loading || upstreamPage * pageSize >= catalog.upstream_total" @click="upstreamPage++; refresh()">{{ text('next') }}</button></div>
          <form class="flex flex-wrap gap-2" @submit.prevent="memberPage = 1; refresh()"><label>{{ text('searchAccount') }}<input v-model="memberSearch" class="input block" maxlength="100" /></label><label>{{ text('members') }}<select v-model="membership" class="input block"><option value="all">{{ text('all') }}</option><option value="assigned">{{ text('assigned') }}</option><option value="unassigned">{{ text('unassigned') }}</option></select></label><button class="btn btn-secondary self-end" :disabled="loading">{{ text('apply') }}</button></form>
          <div class="overflow-x-auto"><table class="w-full min-w-[600px] text-left text-sm"><thead><tr><th>{{ text('account') }}</th><th>{{ text('state') }}</th><th>{{ text('upstream') }}</th><th>{{ text('actions') }}</th></tr></thead><tbody><tr v-for="member in catalog.accounts" :key="member.id" class="border-t dark:border-dark-700"><td>{{ member.name }} #{{ member.id }}<p class="text-gray-500">{{ member.platform }}</p></td><td>{{ member.status }} ? {{ member.concurrency }}<p v-if="!member.eligible" class="text-amber-600">{{ text('ineligible') }}</p></td><td>{{ supplierName(member.upstream_id) }}</td><td><button class="btn btn-secondary" :disabled="busy || loading || !member.eligible" @click="assigning = member; targetUpstream = 0">{{ text('assign') }}</button><button v-if="member.upstream_id" class="btn btn-danger" :disabled="busy || loading" @click="queue({ action: 'detach', account_id: member.id, expected_upstream_id: member.upstream_id, confirm: true })">{{ text('detach') }}</button></td></tr></tbody></table></div>
          <div class="flex gap-3"><button class="btn btn-secondary" :disabled="loading || memberPage <= 1" @click="memberPage--; refresh()">{{ text('previous') }}</button><span>{{ memberPage }} · {{ catalog.total }}</span><button class="btn btn-secondary" :disabled="loading || memberPage * pageSize >= catalog.total" @click="memberPage++; refresh()">{{ text('next') }}</button></div>
        </template>
      </section>
      <BaseDialog :show="!!editing" :title="text('upstream')" @close="!busy && (editing = null)"><form v-if="editing" class="space-y-3" @submit.prevent="saveSupplier"><input v-model="editing.name" :aria-label="text('name')" class="input w-full" required maxlength="100" :disabled="busy" /><p v-if="dialogError" role="alert">{{ dialogError }}</p><button class="btn btn-primary" :disabled="busy || loading">{{ text('save') }}</button></form></BaseDialog>
      <BaseDialog :show="!!assigning" :title="text('assign')" @close="!busy && (assigning = null)"><form class="space-y-3" @submit.prevent="assignMember"><p>{{ assigning?.name }} #{{ assigning?.id }}</p><p>{{ text('targetHint') }}</p><label>{{ text('target') }}<input v-model.number="targetUpstream" type="number" min="1" step="1" class="input w-full" required :disabled="busy" /></label><p v-if="dialogError" role="alert">{{ dialogError }}</p><button class="btn btn-primary" :disabled="busy || loading || !targetUpstream">{{ text('save') }}</button></form></BaseDialog>
      <ConfirmDialog :show="!!pending" :title="text('confirmTitle')" :message="confirmation" :confirm-text="text('confirm')" :danger="true" @cancel="!busy && (pending = null)" @confirm="confirmMutation" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import * as channelsAPI from '@/api/admin/channels'
import * as ops from '@/api/admin/channelOperations'

const { t } = useI18n()
const text = (key: string, params: Record<string, string | number> = {}) => t(`channelOps.${key}`, params)
const localDate = (date: Date) => new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
const from = ref(localDate(new Date(Date.now() - 86400_000))), until = ref(localDate(new Date()))
const channelId = ref(0), upstreamId = ref(0), accountId = ref(0), groupId = ref(0), model = ref(''), modelScope = ref('public')
const channels = ref<channelsAPI.Channel[]>([]), selectedChannel = ref<channelsAPI.Channel | null>(null), channelTotal = ref(0), channelSearch = ref(''), channelPage = ref(1)
const snapshot = ref<ops.Snapshot | null>(null), catalog = ref<ops.Catalog | null>(null), loading = ref(false), busy = ref(false), error = ref(''), dialogError = ref('')
const statsPage = ref(1), memberPage = ref(1), upstreamPage = ref(1), memberSearch = ref(''), upstreamSearch = ref(''), membership = ref('all'), qualityBefore = ref(0), pageSize = 20
const editing = ref<{ id: number; name: string } | null>(null), assigning = ref<ops.Member | null>(null), targetUpstream = ref(0)
const pending = ref<{ channelId: number; body: ops.Mutation } | null>(null)
const channelOptions = computed(() => selectedChannel.value && !channels.value.some(c => c.id === selectedChannel.value?.id) ? [selectedChannel.value, ...channels.value] : channels.value)
const dimension = computed(() => accountId.value ? 'account' : !channelId.value ? 'channel' : upstreamId.value ? 'account' : 'upstream')
const supplierName = (id: number) => id <= 0 ? text('unassigned') : catalog.value?.upstreams.find(u => u.id === id)?.name || `#${id}`
const ms = (value: number | null) => value == null ? '—' : `${(value / 1000).toFixed(2)}s`
const confirmation = computed(() => pending.value ? `${text('confirmMembership')} · ${text('channels')} #${pending.value.channelId} · ${text('account')} #${pending.value.body.account_id || '—'} · ${supplierName(pending.value.body.expected_upstream_id || 0)} → ${supplierName(pending.value.body.upstream_id || 0)} (#${pending.value.body.upstream_id || '—'})` : '')
let controller: AbortController | undefined, revision = 0, alive = true
const message = (e: unknown) => text((e as { status?: number } | null)?.status === 409 ? 'conflict' : 'failed')
async function refresh() {
  const current = ++revision; controller?.abort(); controller = new AbortController(); const signal = controller.signal
  const start = new Date(from.value), end = new Date(until.value)
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || end <= start || end.getTime() - start.getTime() > 31 * 86400_000) { error.value = text('invalidWindow'); loading.value = false; return }
  loading.value = true; error.value = ''; snapshot.value = null; catalog.value = null
  const results = await Promise.allSettled([
    channelsAPI.list(channelPage.value, pageSize, { search: channelSearch.value }, { signal }),
    channelId.value ? channelsAPI.getById(channelId.value) : Promise.resolve(null),
    channelId.value ? ops.catalog(channelId.value, { page: memberPage.value, page_size: pageSize, search: memberSearch.value, upstream_page: upstreamPage.value, upstream_search: upstreamSearch.value, upstream_id: 0, membership: membership.value }, signal) : Promise.resolve(null),
    ops.snapshot({ channel_id: channelId.value, upstream_id: upstreamId.value, account_id: accountId.value, group_id: groupId.value || 0, from: start.toISOString(), until: end.toISOString(), model: model.value.trim(), model_scope: modelScope.value, dimension: dimension.value, page: statsPage.value, page_size: pageSize, quality_before: qualityBefore.value }, signal)
  ])
  if (!alive || current !== revision) return
  const [list, selected, members, stats] = results
  if (list.status === 'fulfilled') { channels.value = list.value.items; channelTotal.value = list.value.total } else error.value = message(list.reason)
  if (selected.status === 'fulfilled') selectedChannel.value = selected.value; else error.value = message(selected.reason)
  if (members.status === 'fulfilled') catalog.value = members.value; else error.value = message(members.reason)
  if (stats.status === 'fulfilled') snapshot.value = stats.value; else error.value = message(stats.reason)
  loading.value = false
}
function applyFilters() { statsPage.value = 1; qualityBefore.value = 0; void refresh() }
function selectChannel() { upstreamId.value = accountId.value = groupId.value = 0; memberPage.value = upstreamPage.value = 1; assigning.value = editing.value = pending.value = null; applyFilters() }
function last24Hours() { const now = new Date(); from.value = localDate(new Date(now.getTime() - 86400_000)); until.value = localDate(now); applyFilters() }
function drill(id: number) { if (dimension.value === 'channel') { channelId.value = id; selectChannel() } else if (dimension.value === 'upstream') { upstreamId.value = id || -1; accountId.value = 0; applyFilters() } }
function olderQuality() { const rows = snapshot.value?.quality.data?.rows; if (rows?.length) { qualityBefore.value = rows[rows.length - 1].id; void refresh() } }
function queue(body: Omit<ops.Mutation, 'revision'>) { if (catalog.value) pending.value = { channelId: channelId.value, body: { ...body, revision: catalog.value.revision } } }
async function mutate(id: number, body: ops.Mutation) {
  if (busy.value) return; busy.value = true; dialogError.value = ''; error.value = ''
  try { await ops.mutate(id, body); editing.value = assigning.value = pending.value = null; await refresh() } catch (e) { const failure = message(e); pending.value = null; await refresh(); error.value = dialogError.value = failure } finally { busy.value = false }
}
function saveSupplier() { if (editing.value && catalog.value) void mutate(channelId.value, { action: editing.value.id ? 'update_upstream' : 'create_upstream', upstream_id: editing.value.id, name: editing.value.name.trim(), revision: catalog.value.revision }) }
function assignMember() { if (!assigning.value || !catalog.value) return; const body: ops.Mutation = { action: 'assign', account_id: assigning.value.id, expected_upstream_id: assigning.value.upstream_id, upstream_id: targetUpstream.value, revision: catalog.value.revision, confirm: true }; queue(body) }
function confirmMutation() { if (pending.value) void mutate(pending.value.channelId, pending.value.body) }
onMounted(refresh)
onBeforeUnmount(() => { alive = false; revision++; controller?.abort() })
</script>

<style scoped>
th, td { padding: .75rem; }
</style>
