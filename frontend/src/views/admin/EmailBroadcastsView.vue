<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h1 data-testid="email-broadcast-title" class="text-xl font-semibold">{{ text('title') }}</h1>
        <RouterLink to="/admin/settings?tab=email" class="btn btn-secondary">{{ text('smtpSettings') }}</RouterLink>
      </div>
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ text('description') }}</p>
      <p v-if="error" role="alert" class="break-words text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="notice" role="status" class="text-green-700 dark:text-green-400">{{ notice }}</p>
      <form class="card space-y-4 p-4 sm:p-6" @submit.prevent="prepareSend">
        <div class="grid gap-5 lg:grid-cols-2">
          <div class="space-y-3">
            <label for="broadcast-recipients" class="input-label">{{ text('recipients') }}</label>
            <textarea id="broadcast-recipients" v-model="recipients" rows="8" maxlength="1048576" class="input w-full" :placeholder="text('recipientPlaceholder')" />
            <p class="input-hint">{{ text('recipientHint') }}</p>
            <div class="flex flex-wrap items-center gap-2">
              <input ref="fileInput" data-testid="broadcast-file" type="file" accept=".txt,.csv,.xlsx,.xls" class="hidden" @change="importFile" />
              <button type="button" class="btn btn-secondary" :disabled="busy" @click="fileInput?.click()">{{ text('importFile') }}</button>
              <button type="button" class="btn btn-secondary" :disabled="busy || !recipients.trim()" @click="previewRecipients">{{ text('previewRecipients') }}</button>
            </div>
            <p class="input-hint">{{ text('fileHint') }}</p>
            <div v-if="preview" class="space-y-2 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700" aria-live="polite">
              <p>{{ text('previewCounts', { valid: preview.recipients.length, duplicates: preview.duplicates, invalid: preview.invalid.length }) }}</p>
              <details v-if="preview.recipients.length"><summary class="cursor-pointer">{{ text('convertedAddresses') }}</summary><div class="mt-2 max-h-40 overflow-y-auto whitespace-pre-line break-all">{{ preview.recipients.join('\n') }}</div></details>
              <template v-if="preview.invalid.length">
                <p class="text-red-600 dark:text-red-400">{{ text('invalidHint') }}</p>
                <div class="max-h-28 overflow-y-auto whitespace-pre-line break-all">{{ preview.invalid.join('\n') }}</div>
                <button type="button" class="btn btn-secondary" :disabled="busy || !preview.recipients.length" @click="useValidRecipients">{{ text('useValid') }}</button>
              </template>
            </div>
          </div>
          <div class="space-y-3">
            <label for="broadcast-subject" class="input-label">{{ text('subject') }}</label>
            <input id="broadcast-subject" v-model="subject" class="input w-full" maxlength="200" required />
            <div class="flex flex-wrap items-end justify-between gap-2">
              <label for="broadcast-content" class="input-label">{{ text('content') }}</label>
              <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="text('contentFormat')">
                <button type="button" class="btn btn-secondary" :class="{ 'ring-2 ring-primary-500': contentFormat === 'text' }" :aria-pressed="contentFormat === 'text' ? 'true' : 'false'" @click="contentFormat = 'text'">{{ text('plainText') }}</button>
                <button type="button" class="btn btn-secondary" :class="{ 'ring-2 ring-primary-500': contentFormat === 'html' }" :aria-pressed="contentFormat === 'html' ? 'true' : 'false'" @click="contentFormat = 'html'">{{ text('html') }}</button>
                <button type="button" class="btn btn-secondary" @click="loadPromotionTemplate">{{ text('loadTemplate') }}</button>
              </div>
            </div>
            <textarea v-if="contentFormat === 'text'" id="broadcast-content" v-model="content" rows="10" maxlength="100000" class="input w-full font-mono text-sm" required />
            <p class="input-hint">{{ text(contentFormat === 'html' ? 'htmlHint' : 'contentHint') }}</p>
            <template v-if="contentFormat === 'html'">
              <div v-if="content.trim()" class="space-y-2">
                <p class="input-label">{{ text('htmlPreview') }}</p>
                <iframe :srcdoc="htmlPreview" sandbox="" :title="text('htmlPreview')" class="h-[520px] w-full rounded-lg border border-gray-200 bg-white dark:border-dark-600" />
              </div>
              <details>
                <summary class="cursor-pointer text-sm font-medium">{{ text('htmlSource') }}</summary>
                <label for="broadcast-content" class="sr-only">{{ text('htmlSource') }}</label>
                <textarea id="broadcast-content" v-model="content" rows="10" maxlength="100000" class="input mt-2 w-full font-mono text-sm" :placeholder="text('htmlPlaceholder')" required />
              </details>
              <label for="broadcast-unsubscribe-url" class="input-label">{{ text('unsubscribeUrl') }}</label>
              <input id="broadcast-unsubscribe-url" v-model="unsubscribeUrl" type="url" class="input w-full" :placeholder="text('unsubscribePlaceholder')" />
              <p class="input-hint">{{ text('unsubscribeHint', { placeholder: unsubscribePlaceholder }) }}</p>
            </template>
            <label for="broadcast-interval" class="input-label">{{ text('interval') }}</label>
            <input id="broadcast-interval" v-model.number="interval" type="number" min="1" max="60" step="1" class="input w-28" required />
            <p class="input-hint">{{ text('intervalHint') }}</p>
          </div>
        </div>
        <div class="flex flex-wrap items-end gap-3 border-t pt-4 dark:border-dark-600">
          <label class="flex-1" for="broadcast-test-recipient"><span class="input-label">{{ text('testRecipient') }}</span><input id="broadcast-test-recipient" v-model="testRecipient" class="input w-full" :placeholder="text('testPlaceholder')" /></label>
          <button type="button" class="btn btn-secondary" :disabled="busy || !testRecipient.trim() || !subject.trim() || !content.trim()" @click="sendTest">{{ text('sendTest') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="busy || !recipients.trim()">{{ busy ? text('working') : text('reviewSend') }}</button>
        </div>
      </form>

      <section class="card space-y-3 p-4 sm:p-6">
        <div class="flex items-center justify-between"><h2 class="font-semibold">{{ text('history') }}</h2><button type="button" class="btn btn-secondary" :disabled="refreshing" @click="refresh">{{ text('refresh') }}</button></div>
        <p class="input-hint">{{ text('deliveryHint') }}</p>
        <p v-if="!items.length" class="text-sm text-gray-500 dark:text-gray-400">{{ text('emptyHistory') }}</p>
        <div v-for="item in items" :key="item.id" class="flex flex-wrap items-center gap-3 border-t py-3 dark:border-dark-600">
          <button type="button" class="min-w-0 flex-1 text-left" @click="openDetail(item.id)"><span class="block break-words font-medium text-primary-700 dark:text-primary-400">{{ item.subject }}</span><span class="text-xs text-gray-500 dark:text-gray-400">{{ new Date(item.created_at).toLocaleString() }} · {{ statusLabel(item.status) }}</span></button>
          <span class="text-sm">{{ text('progress', counts(item)) }}</span>
          <button v-if="active(item)" type="button" class="btn btn-secondary" :disabled="busy" @click="cancelJob(item.id)">{{ text('stop') }}</button>
        </div>
        <div class="flex items-center gap-3"><button class="btn btn-secondary" :disabled="refreshing || historyPage <= 1" @click="historyPage--; refresh()">{{ text('previous') }}</button><span>{{ historyPage }}</span><button class="btn btn-secondary" :disabled="refreshing || items.length < 20" @click="historyPage++; refresh()">{{ text('next') }}</button></div>
      </section>

      <section v-if="detail" class="card space-y-3 p-4 sm:p-6" data-testid="broadcast-detail">
        <h2 class="break-words font-semibold">{{ detail.broadcast.subject }} · {{ statusLabel(detail.broadcast.status) }}</h2>
        <p class="text-sm">{{ text('detailCounts', counts(detail.broadcast)) }}</p>
        <details><summary class="cursor-pointer">{{ text('mailContent') }}</summary><p class="mt-2 whitespace-pre-wrap break-words text-sm">{{ detail.broadcast.content }}</p></details>
        <p class="input-hint">{{ text('unknownHint') }}</p>
        <label for="broadcast-status" class="input-label">{{ text('filter') }}</label>
        <select id="broadcast-status" v-model="recipientStatus" class="input" @change="recipientPage = 1; refresh()"><option value="">{{ text('all') }}</option><option v-for="status in recipientStatuses" :key="status" :value="status">{{ statusLabel(status) }}</option></select>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr><th class="py-2">{{ text('email') }}</th><th>{{ text('status') }}</th><th>{{ text('result') }}</th></tr></thead><tbody><tr v-for="recipient in detail.recipients" :key="recipient.email" class="border-t dark:border-dark-600"><td class="break-all py-2">{{ recipient.email }}</td><td class="whitespace-nowrap px-2">{{ statusLabel(recipient.status) }}</td><td>{{ recipient.error ? text(recipient.status === 'unknown' ? 'interrupted' : 'smtpFailed') : '—' }}</td></tr></tbody></table></div>
        <div class="flex items-center gap-3"><button class="btn btn-secondary" :disabled="refreshing || recipientPage <= 1" @click="recipientPage--; refresh()">{{ text('previous') }}</button><span>{{ recipientPage }} · {{ detail.total }}</span><button class="btn btn-secondary" :disabled="refreshing || recipientPage * 100 >= detail.total" @click="recipientPage++; refresh()">{{ text('next') }}</button></div>
      </section>
    </div>
    <BaseDialog :show="!!pending" :title="text('confirmTitle')" @close="closeConfirmation">
      <template v-if="pending">
        <p class="mb-3 font-medium">{{ text('confirmCount', { count: preview?.recipients.length || 0 }) }}</p>
        <p class="mb-2 break-words font-semibold">{{ pending.subject }}</p>
        <p class="mb-2 text-sm">{{ text('format') }}: {{ text(pending.content_format === 'html' ? 'html' : 'plainText') }}</p>
        <p v-if="pending.content_format === 'text'" class="max-h-64 overflow-y-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700">{{ pending.content }}</p>
        <iframe v-else :srcdoc="previewHtml(pending.content, pending.unsubscribe_url)" sandbox="" :title="text('htmlPreview')" class="h-[420px] w-full rounded-lg border border-gray-200 bg-white dark:border-dark-600" />
        <p class="mt-3 text-sm">{{ text('confirmHint', { interval: pending.interval_seconds }) }}</p>
        <p v-if="submitError" role="alert" class="mt-3 text-red-600 dark:text-red-400">{{ submitError }}</p>
      </template>
      <template #footer><div class="flex justify-end gap-3"><button class="btn btn-secondary" :disabled="busy" @click="closeConfirmation">{{ text('back') }}</button><button data-testid="broadcast-confirm" class="btn btn-primary" :disabled="busy" @click="submit">{{ busy ? text('working') : text('confirmSend') }}</button></div></template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import * as api from '@/api/admin/emailBroadcasts'
import { importBroadcastRecipients } from '@/utils/emailBroadcastImport'
import promotionTemplate from '@/assets/templates/91token-promotion.html?raw'

const unsubscribePlaceholder = '{{unsubscribe_url}}'
const { t } = useI18n()
const text = (key: string, params: Record<string, string | number> = {}) => t(`emailBroadcast.${key}`, params)
const recipients = ref(''), subject = ref(text('promotionSubject')), content = ref(promotionTemplate), contentFormat = ref<api.BroadcastContentFormat>('html'), unsubscribeUrl = ref(''), interval = ref(2), testRecipient = ref('')
const fileInput = ref<HTMLInputElement | null>(null), preview = ref<api.RecipientPreview | null>(null)
const busy = ref(false), refreshing = ref(false), error = ref(''), notice = ref(''), submitError = ref('')
const items = ref<api.Broadcast[]>([]), detail = ref<api.BroadcastDetail | null>(null), selectedID = ref('')
const pending = ref<api.BroadcastInput | null>(null), historyPage = ref(1), recipientPage = ref(1), recipientStatus = ref('')
const recipientStatuses = ['pending', 'sending', 'sent', 'failed', 'unknown', 'cancelled']
const active = (item: api.Broadcast) => ['queued', 'running'].includes(item.status)
const counts = (item: api.Broadcast) => ({ total: item.total, pending: item.pending, sending: item.sending, sent: item.sent, failed: item.failed, unknown: item.unknown, cancelled: item.cancelled })
const statusLabel = (status: string) => text(`statuses.${status}`)
const failure = (e: unknown) => (e as { message?: string })?.message || text('failed')
let requestID = '', timer: ReturnType<typeof setInterval> | undefined, alive = true, refreshVersion = 0
watch(recipients, () => { preview.value = null; requestID = '' }, { flush: 'sync' })
watch([subject, content, contentFormat, unsubscribeUrl, interval], () => { requestID = '' }, { flush: 'sync' })

function escapeHtmlAttribute(value: string) {
  return value.replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character)
}
function isHttpsUrl(value: string) {
  try { const url = new URL(value); return url.protocol === 'https:' && !!url.hostname && !url.username && !url.password } catch { return false }
}
function previewHtml(source: string, url = unsubscribeUrl.value) {
  url = url.trim()
  if (!url) source = source.replace(/<!-- sub2api:unsubscribe:start -->[\s\S]*?<!-- sub2api:unsubscribe:end -->/g, '')
  const replacement = url && isHttpsUrl(url) ? escapeHtmlAttribute(url) : ''
  return source.split(unsubscribePlaceholder).join(replacement)
}
const htmlPreview = computed(() => previewHtml(content.value))
function resolvedContent() {
  if (contentFormat.value !== 'html') return content.value
  if (content.value.includes(unsubscribePlaceholder) && unsubscribeUrl.value.trim() && !isHttpsUrl(unsubscribeUrl.value.trim())) throw new Error('unsubscribeUrlRequired')
  return content.value
}
function loadPromotionTemplate() {
  contentFormat.value = 'html'
  content.value = promotionTemplate
  if (!subject.value.trim()) subject.value = text('promotionSubject')
  notice.value = text('templateLoaded')
}

async function fetchPreview() {
  const source = recipients.value
  const result = await api.preview(source)
  if (!alive || recipients.value !== source) return null
  preview.value = result
  return result
}
async function previewRecipients() {
  busy.value = true; error.value = ''; notice.value = ''
  try { await fetchPreview() } catch (e) { error.value = failure(e) } finally { busy.value = false }
}
function useValidRecipients() {
  if (!preview.value) return
  const result = preview.value
  recipients.value = result.recipients.join('\n')
  preview.value = { ...result, invalid: [] }
}
async function importFile(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  if (!file) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const imported = await importBroadcastRecipients(file)
    if (new TextEncoder().encode(recipients.value + '\n' + imported).length > 1048576) throw new Error('fileTooLarge')
    recipients.value = [recipients.value, imported].filter(Boolean).join('\n')
    await fetchPreview()
  } catch (e) {
    const code = (e as Error).message
    error.value = ['fileTooLarge', 'emptyFile', 'tooManyRows', 'fileType'].includes(code) ? text(code) : text('fileFailed')
  } finally { input.value = ''; busy.value = false }
}
async function prepareSend() {
  busy.value = true; error.value = ''; notice.value = ''; submitError.value = ''
  try {
    const result = preview.value || await fetchPreview()
    if (!result) return
    if (!result.recipients.length || result.invalid.length) { error.value = text('invalidHint'); return }
    const message = resolvedContent()
    if (new TextEncoder().encode(message).length > 100000) { error.value = text('contentTooLarge'); return }
    requestID ||= crypto.randomUUID()
    pending.value = { id: requestID, recipients: result.recipients.join('\n'), subject: subject.value.trim(), content: message, content_format: contentFormat.value, unsubscribe_url: unsubscribeUrl.value.trim(), interval_seconds: interval.value }
  } catch (e) { error.value = (e as Error).message === 'unsubscribeUrlRequired' ? text('unsubscribeUrlRequired') : failure(e) } finally { busy.value = false }
}
function closeConfirmation() { if (!busy.value) pending.value = null }
async function submit() {
  if (!pending.value || busy.value) return
  busy.value = true; submitError.value = ''
  try {
    const result = await api.create(pending.value)
    selectedID.value = result.broadcast.id; recipientPage.value = 1; recipientStatus.value = ''; historyPage.value = 1
    detail.value = result; pending.value = null; notice.value = text('queued')
    recipients.value = ''; subject.value = text('promotionSubject'); content.value = promotionTemplate; contentFormat.value = 'html'; unsubscribeUrl.value = ''; requestID = ''
    await refresh()
  } catch (e) { submitError.value = failure(e) } finally { busy.value = false }
}
async function sendTest() {
  busy.value = true; error.value = ''; notice.value = ''
  try { await api.test(testRecipient.value, subject.value, resolvedContent(), contentFormat.value, unsubscribeUrl.value.trim()); notice.value = text('testSent') }
  catch (e) { error.value = (e as Error).message === 'unsubscribeUrlRequired' ? text('unsubscribeUrlRequired') : failure(e) } finally { busy.value = false }
}
async function cancelJob(id: string) {
  busy.value = true; error.value = ''
  try { await api.cancel(id); notice.value = text('stopped'); await refresh() }
  catch (e) { error.value = failure(e) } finally { busy.value = false }
}
function openDetail(id: string) { selectedID.value = id; recipientPage.value = 1; recipientStatus.value = ''; detail.value = null; void refresh() }
async function refresh() {
  const version = ++refreshVersion, id = selectedID.value
  refreshing.value = true
  const results = await Promise.allSettled([api.list(historyPage.value), id ? api.detail(id, recipientStatus.value, recipientPage.value) : Promise.resolve(null)])
  if (!alive || version !== refreshVersion) return
  if (results[0].status === 'fulfilled') items.value = results[0].value.items
  else error.value = failure(results[0].reason)
  if (results[1].status === 'fulfilled') detail.value = results[1].value
  else error.value = failure(results[1].reason)
  refreshing.value = false
}
onMounted(() => {
  void refresh()
  timer = setInterval(() => { if (!document.hidden && !refreshing.value && (items.value.some(active) || (detail.value && (active(detail.value.broadcast) || detail.value.broadcast.sending > 0)))) void refresh() }, 5000)
})
onBeforeUnmount(() => { alive = false; if (timer) clearInterval(timer) })
</script>
