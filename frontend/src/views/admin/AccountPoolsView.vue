<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ tr('accountPoolManagement') }}</h1>
        <span v-if="exportEnabled !== undefined" :class="['badge', exportEnabled ? 'badge-success' : 'badge-gray']">
          {{ tr(exportEnabled ? 'accountPoolExportEnabled' : 'accountPoolExportDisabled') }}
        </span>
      </header>
      <nav class="inline-flex flex-wrap gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-800" :aria-label="tr('accountPoolManagement')">
        <button v-for="item in modes" :key="item.value" type="button" :aria-pressed="mode === item.value"
          :class="['rounded-md px-3 py-1.5 text-sm', mode === item.value ? 'bg-white font-medium shadow-sm dark:bg-dark-600' : 'text-gray-600 dark:text-dark-300']"
          @click="mode = item.value">{{ tr(item.label) }}</button>
      </nav>

      <section v-show="mode === 'settings'" class="space-y-5">
        <div class="space-y-3 border-b border-gray-200 pb-5 dark:border-dark-700">
          <h2 class="text-base font-semibold">{{ tr('accountPoolPublish') }}</h2>
          <div class="flex flex-wrap items-center gap-2">
            <code class="min-w-0 break-all rounded bg-gray-100 px-2 py-1.5 text-xs dark:bg-dark-800">GET {{ endpoint }}</code>
            <button class="btn btn-secondary" type="button" :aria-label="tr('accountPoolCopyURL')" :title="tr('accountPoolCopyURL')" @click="copyToClipboard(endpoint)"><Icon name="copy" size="sm" /></button>
          </div>
          <div class="flex flex-wrap gap-2" v-if="exportEnabled !== undefined">
            <button class="btn btn-primary" type="button" :disabled="tokenBusy || (!exportEnabled && !preview?.pools.some(pool => pool.snapshot_available))" @click="exportEnabled ? pending = 'issue' : void changeExport('issue')">
              <Icon name="key" size="sm" />{{ tr(exportEnabled ? 'accountPoolRotateToken' : 'accountPoolEnableExport') }}
            </button>
            <button v-if="exportEnabled" class="btn btn-secondary" type="button" :disabled="tokenBusy" @click="pending = 'disable'">{{ tr('accountPoolDisableExport') }}</button>
          </div>
          <p v-if="exportError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ exportError }}</p>
          <div v-if="token" class="space-y-1" role="status">
            <p class="text-sm font-medium">{{ tr('accountPoolTokenOnce') }}</p>
            <div class="flex flex-wrap items-center gap-2">
              <code class="min-w-0 break-all rounded bg-gray-100 px-2 py-1.5 text-xs dark:bg-dark-800">{{ token }}</code>
              <button class="btn btn-secondary" type="button" :aria-label="tr('accountPoolCopyToken')" :title="tr('accountPoolCopyToken')" @click="copyToClipboard(token)"><Icon name="copy" size="sm" /></button>
            </div>
          </div>
        </div>
        <p v-if="sourceError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ sourceError }}</p>
        <p v-else-if="!sourcesLoaded" class="text-sm text-gray-500" role="status">{{ tr('accountPoolLoading') }}</p>
        <p v-else-if="!sources.length" class="text-sm text-gray-500">{{ tr('accountPoolNoSource') }}</p>
        <div v-for="entry in sources" :key="entry.id" class="max-w-3xl">
          <h2 class="text-base font-semibold">{{ entry.name }}</h2>
          <p v-if="!entry.snapshot.updated" class="mt-2 text-sm text-gray-500">{{ tr('accountPoolEmpty') }}</p>
          <UpstreamPoolStatusForm :site-id="entry.id" :snapshot="entry.snapshot" @saved="loadPreview(1)" />
        </div>
      </section>

      <section v-show="mode === 'preview'" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-base font-semibold">{{ tr('accountPoolShowcase') }}</h2>
          <button class="btn btn-secondary" type="button" :disabled="previewBusy" @click="loadPreview(preview?.page ?? 1)"><Icon name="refresh" size="sm" />{{ tr('accountPoolReload') }}</button>
        </div>
        <p v-if="previewError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ previewError }}</p>
        <p v-else-if="!preview" class="text-sm text-gray-500" role="status">{{ tr('accountPoolLoading') }}</p>
        <p v-else-if="!preview.pools.length" class="text-sm text-gray-500">{{ tr('accountPoolNoPublished') }}</p>
        <article v-for="pool in preview?.pools ?? []" :key="pool.id" class="min-w-0 space-y-3 border-b border-gray-200 pb-5 dark:border-dark-700">
          <h3 class="break-words font-semibold">{{ pool.name }} <span class="text-sm font-normal text-gray-500">{{ pool.source }}</span></h3>
          <p v-if="!pool.snapshot_available" class="text-sm text-amber-700 dark:text-amber-300">{{ tr('accountPoolEmpty') }}</p>
          <template v-else>
            <ul class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-700 dark:text-gray-300" :aria-label="tr('accountPoolPlans')">
              <li v-for="(count, plan) in pool.plans" :key="plan" class="break-words">{{ plan }} <span class="tabular-nums text-gray-500 dark:text-gray-400">{{ count }}</span></li>
            </ul>
            <UpstreamAccountPoolPanel :published="pool" />
          </template>
        </article>
        <div v-if="preview && pageCount > 1" class="flex flex-wrap items-center justify-end gap-2 text-sm">
          <button class="btn btn-secondary" type="button" :disabled="previewBusy || preview.page <= 1" :aria-label="tr('accountPoolPrevious')" :title="tr('accountPoolPrevious')" @click="loadPreview(preview.page - 1)">‹</button>
          <span class="px-1 tabular-nums" aria-live="polite">{{ preview.page }} / {{ pageCount }}</span>
          <button class="btn btn-secondary" type="button" :disabled="previewBusy || preview.page >= pageCount" :aria-label="tr('accountPoolNext')" :title="tr('accountPoolNext')" @click="loadPreview(preview.page + 1)">›</button>
        </div>
      </section>
      <ConfirmDialog :show="pending !== null" :title="tr(pending === 'disable' ? 'accountPoolDisableExport' : 'accountPoolRotateToken')"
        :message="tr('accountPoolReplaceToken')" danger @cancel="pending = null" @confirm="confirmExport" />
      <TotpStepUpDialog :controller="stepUp" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import UpstreamAccountPoolPanel from '@/components/admin/account/UpstreamAccountPoolPanel.vue'
import UpstreamPoolStatusForm from '@/components/admin/account/UpstreamPoolStatusForm.vue'
import { useClipboard } from '@/composables/useClipboard'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { accountPoolStatusExportPath, siteError, upstreamSitesAPI as api, type PoolStatusPreview, type SitePoolDisplay } from '@/api/admin/upstreamSites'

const { t } = useI18n()
const tr = (key: string, values: Record<string, string | number> = {}) => t(`upstreamSites.${key}`, values)
const endpoint = window.location.origin + accountPoolStatusExportPath
const modes = [{ value: 'preview', label: 'accountPoolShowcase' }, { value: 'settings', label: 'accountPoolExportConfig' }] as const
const mode = ref<'settings' | 'preview'>('preview')
const preview = shallowRef<PoolStatusPreview>()
const sources = shallowRef<{ id: string; name: string; snapshot: SitePoolDisplay }[]>([])
const sourcesLoaded = ref(false), previewBusy = ref(false), tokenBusy = ref(false)
const previewError = ref(''), sourceError = ref(''), exportError = ref('')
const exportEnabled = ref<boolean>(), token = ref('')
type ExportAction = 'issue' | 'disable'
const pending = ref<ExportAction | null>(null)
const pageCount = computed(() => Math.max(1, ...((preview.value?.pools ?? []).map(pool => pool.pages))))
const { copyToClipboard } = useClipboard()
const stepUp = useStepUp()

async function loadSources() {
  try {
    const sites = (await api.list()).filter(site => site.purpose === 'public' && site.kind === 'sub2api' && site.base_url === 'https://aishenji.top')
    sources.value = await Promise.all(sites.map(async site => ({ id: site.id, name: site.name, snapshot: await api.accountPool(site.id, { page: 1, page_size: 1 }) })))
  } catch (error) { sourceError.value = siteError(error) }
  finally { sourcesLoaded.value = true }
}

let previewRequest = 0
async function loadPreview(page: number) {
  const request = ++previewRequest
  previewBusy.value = true; previewError.value = ''
  try {
    const data = await api.previewPoolStatus(page, 30)
    if (request === previewRequest) preview.value = data
  } catch (error) { if (request === previewRequest) previewError.value = siteError(error) }
  finally { if (request === previewRequest) previewBusy.value = false }
}

async function loadExportState() {
  try { exportEnabled.value = (await api.poolStatusExport()).enabled }
  catch (error) { exportError.value = siteError(error) }
}

function confirmExport() {
  const action = pending.value
  pending.value = null
  if (action) void changeExport(action)
}

async function changeExport(action: ExportAction) {
  if (tokenBusy.value) return
  tokenBusy.value = true; exportError.value = ''
  try {
    if (action === 'issue') {
      const result = await stepUp.run(() => api.issuePoolStatusToken())
      exportEnabled.value = result.enabled; token.value = result.token
    } else {
      const result = await stepUp.run(() => api.disablePoolStatusExport())
      exportEnabled.value = result.enabled; token.value = ''
    }
  } catch (error) { if (!isStepUpCancelled(error)) exportError.value = siteError(error) }
  finally { tokenBusy.value = false }
}

onMounted(() => { void loadPreview(1); void loadSources(); void loadExportState() })
</script>
