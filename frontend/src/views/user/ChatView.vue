<template>
  <AppLayout>
    <div class="chat-shell">
      <aside class="chat-history hidden lg:flex">
        <div class="flex items-center gap-2 px-2 py-3 text-sm font-semibold text-gray-700 dark:text-gray-200">
          <Icon name="chat" size="md" class="text-primary-500" aria-hidden="true" />
          {{ t('chat.title') }}
        </div>
        <button class="btn btn-primary w-full" :disabled="busy" @click="newChat">+ {{ t('chat.newChat') }}</button>
        <input v-model="historySearch" class="input mt-3 text-sm" type="search" :placeholder="t('chat.searchHistory')" :aria-label="t('chat.searchHistory')" />
        <h2 class="mb-2 mt-7 px-2 text-xs font-medium text-gray-400">{{ t('chat.conversations') }}</h2>
        <nav class="min-h-0 flex-1 space-y-1 overflow-y-auto" :aria-label="t('chat.conversations')">
          <button v-for="chat in visibleChats" :key="chat.id" class="chat-history-item" :class="{ 'chat-history-active': selectedId === chat.id }" :disabled="busy" :aria-current="selectedId === chat.id ? 'page' : undefined" @click="selectedId = chat.id">
            <span class="block truncate">{{ chat.title || t('chat.untitled') }}</span>
            <span v-if="chat.model" class="mt-1 block truncate text-xs text-gray-400">{{ chat.model }}</span>
          </button>
        </nav>
        <p class="px-2 pt-4 text-xs leading-5 text-gray-400">{{ t('chat.historyHint') }}</p>
      </aside>

      <section class="flex min-h-0 min-w-0 flex-1 flex-col">
        <header class="chat-toolbar">
          <div class="mb-4 flex items-center justify-between gap-2 lg:hidden">
            <select v-model="selectedId" class="input min-w-0 flex-1" :aria-label="t('chat.conversations')" :disabled="busy"><option v-for="chat in sortedChats" :key="chat.id" :value="chat.id">{{ chat.title || t('chat.untitled') }}</option></select>
            <button class="btn btn-secondary shrink-0" :disabled="busy" @click="newChat">+ {{ t('chat.newChat') }}</button>
          </div>
          <div v-if="current" class="flex flex-wrap items-center gap-3">
            <div class="min-w-0 flex-1 basis-full sm:basis-0">
              <input v-model="current.title" class="w-full truncate bg-transparent text-lg font-semibold text-gray-900 outline-none focus:underline dark:text-white" :placeholder="t('chat.workspaceTitle')" :aria-label="t('chat.rename')" maxlength="80" :disabled="busy" @change="persist" />
              <p class="mt-1 hidden text-xs text-gray-500 sm:block">{{ t('chat.workspaceHint') }}</p>
            </div>
            <label class="chat-field min-w-0 flex-1 sm:w-60 sm:flex-none"><span>{{ t('chat.key') }}</span>
              <select v-model="current.keyId" class="input" data-testid="chat-key" :disabled="busy || keysLoading" @change="current.model = ''; current.effort = ''">
                <option :value="null">{{ keysLoading ? t('chat.loading') : t('chat.selectKey') }}</option>
                <option v-for="key in keys" :key="key.id" :value="key.id">{{ key.group?.name || '#' + key.group_id }} · {{ key.name }}</option>
              </select>
            </label>
            <button class="btn btn-secondary text-xs" :disabled="!current.messages.length || busy" data-testid="chat-export" @click="exportConversation">{{ t('chat.exportChat') }}</button>
            <button class="chat-icon-button" :disabled="busy" :title="t('chat.deleteChat')" :aria-label="t('chat.deleteChat')" @click="deleteChat">×</button>
          </div>
          <details v-if="current" class="mt-3 text-xs text-gray-500">
            <summary class="w-fit cursor-pointer" data-testid="chat-settings">{{ t('chat.settings') }} · {{ current.model || t('chat.selectModel') }}</summary>
            <div class="mt-3 flex flex-wrap items-end gap-3">
            <label class="chat-field min-w-48 flex-1"><span>{{ t('chat.model') }}</span>
              <input v-model.trim="current.model" class="input" list="chat-models" :placeholder="modelsLoading ? t('chat.loading') : t('chat.selectModel')" :disabled="busy || !current.keyId" maxlength="200" autocomplete="off" data-testid="chat-model" />
              <datalist id="chat-models"><option v-for="model in models" :key="model" :value="model" /></datalist>
            </label>
            <label class="chat-field w-32"><span>{{ t('chat.effort') }}</span>
              <select v-model="current.effort" class="input" :disabled="busy"><option value="">{{ t('chat.default') }}</option><option v-for="effort in ['low', 'medium', 'high', 'xhigh']" :key="effort" :value="effort">{{ t('chat.efforts.' + effort) }}</option></select>
            </label>
            <label class="chat-field w-36"><span>{{ t('chat.outputLength') }}</span><select v-model="current.maxOutputTokens" class="input" :disabled="busy"><option :value="4096">{{ t('chat.outputShort') }}</option><option :value="8192">{{ t('chat.outputStandard') }}</option><option :value="16384">{{ t('chat.outputLong') }}</option></select></label>
            <label class="mb-2 flex items-center gap-2"><input v-model="current.financeMode" type="checkbox" :disabled="busy" data-testid="chat-finance-mode" />{{ t('chat.financeMode') }}</label>
            <button class="chat-icon-button" :disabled="busy || keysLoading" :title="t('chat.reload')" :aria-label="t('chat.reload')" @click="loadKeys">↻</button>
            </div>
          </details>
          <p v-if="!keysLoading && !keys.length && !keysError" class="mt-3 text-sm text-gray-500">{{ t('chat.noKeys') }} <RouterLink to="/keys" class="text-primary-600 underline">{{ t('chat.manageKeys') }}</RouterLink></p>
          <p v-if="keysError || modelsError" role="alert" class="mt-3 text-sm text-amber-600">{{ t(keysError ? 'chat.keysError' : 'chat.modelsError') }}</p>
        </header>

        <div ref="messagePane" class="min-h-0 flex-1 overflow-y-auto" role="log" :aria-label="t('chat.title')" :aria-busy="busy" @scroll="onScroll">
          <div v-if="!current?.messages.length" class="flex h-full min-h-64 flex-col items-center justify-center px-6 py-12 text-center">
            <div class="mb-6 flex h-14 w-14 items-center justify-center rounded-2xl bg-primary-50 text-2xl text-primary-600 dark:bg-primary-900/30">✦</div>
            <h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('chat.emptyTitle') }}</h1>
            <p class="mt-3 text-sm text-gray-500">{{ t('chat.emptyHint') }}</p>
            <div class="mt-7 grid w-full max-w-2xl gap-3 text-left sm:grid-cols-2">
              <button v-for="task in tasks" :key="task" class="chat-task" :disabled="busy" :data-testid="'chat-task-' + task" @click="useTask(task)">
                <span class="block text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('chat.tasks.' + task + '.title') }}</span>
                <span class="mt-1 block text-xs leading-5 text-gray-500">{{ t('chat.tasks.' + task + '.hint') }}</span>
              </button>
            </div>
          </div>
          <div v-else class="mx-auto max-w-4xl space-y-7 px-4 py-8 md:px-8">
            <article v-for="message in current.messages" :key="message.id" :class="message.role === 'user' ? 'ml-auto max-w-[90%]' : 'mr-auto w-full'">
              <div class="mb-2 text-xs font-medium text-gray-400" :class="{ 'text-right': message.role === 'user' }">{{ t(message.role === 'user' ? 'chat.you' : 'chat.assistant') }}</div>
              <template v-if="message.role === 'user'">
                <p class="ml-auto w-fit whitespace-pre-wrap break-words rounded-2xl rounded-tr-sm bg-primary-50 px-5 py-3 text-sm leading-7 text-gray-800 dark:bg-primary-900/25 dark:text-gray-100">{{ message.content }}</p>
                <details v-for="(file, index) in message.attachments" :key="index" class="chat-source ml-auto mt-2">
                  <summary class="cursor-pointer break-all text-xs text-gray-500">{{ file.name }} · {{ t('chat.sourceFile') }}</summary>
                  <pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words text-xs">{{ file.content }}</pre>
                </details>
              </template>
              <template v-else>
                <details v-if="message.reasoning" class="mb-3 rounded-xl border border-gray-100 px-4 py-3 dark:border-dark-700"><summary class="cursor-pointer text-xs text-gray-500">{{ t('chat.thinking') }}</summary><p class="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-gray-500">{{ message.reasoning }}</p></details>
                <!-- Only sanitized model Markdown is rendered as HTML. -->
                <div v-if="message.content" class="chat-markdown" v-html="chatMarkdown(message.content)"></div>
                <p v-if="message.status === 'streaming' && !message.content" class="animate-pulse py-2 text-sm text-gray-400" role="status">{{ t('chat.waiting') }}</p>
                <p v-if="['stopped', 'failed', 'limited'].includes(message.status)" class="mt-3 text-xs text-amber-600">{{ t('chat.' + message.status) }}</p>
                <div v-if="message.content && message.status !== 'streaming'" class="mt-3 flex flex-wrap items-center gap-4 text-xs text-gray-500">
                  <button @click="copy(message.content)">{{ t('chat.copy') }}</button>
                  <button @click="downloadReport(message.content)">{{ t('chat.downloadReport') }}</button>
                  <button v-if="hasTables(message.content)" :disabled="exporting" data-testid="chat-download-excel" @click="downloadTables(message.content)">{{ t('chat.downloadExcel') }}</button>
                  <button v-if="hasTables(message.content)" data-testid="chat-copy-table" @click="copyTable(message.content)">{{ t('chat.copyTable') }}</button>
                </div>
              </template>
            </article>
          </div>
        </div>

        <div class="mx-auto w-full max-w-4xl px-4 pb-4 pt-2 md:px-8">
          <p v-if="error" role="alert" class="mb-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
          <p v-if="storageError" role="status" class="mb-2 text-xs text-amber-600">{{ t('chat.storageError') }}</p>
          <button v-if="current?.messages.at(-1)?.role === 'assistant' && !busy" class="mb-3 text-xs font-medium text-gray-500 hover:text-primary-600" :disabled="!canSend" data-testid="chat-retry" @click="send(true)">↻ {{ t('chat.retry') }}</button>
          <div class="mb-2 flex flex-wrap items-center gap-2 text-xs text-gray-500">
            <span>{{ t('chat.quickTasks') }}</span><button v-for="task in tasks" :key="task" class="rounded-md px-2 py-1 hover:bg-gray-100 dark:hover:bg-dark-800" :disabled="busy" @click="useTask(task)">{{ t('chat.tasks.' + task + '.title') }}</button>
          </div>
          <form class="chat-composer" :class="{ 'ring-2 ring-primary-400': dragging }" @submit.prevent="send(false)" @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false" @drop.prevent="dropFiles">
            <div v-if="draftFiles.length" class="space-y-2 px-4 pt-3">
              <div v-for="(file, index) in draftFiles" :key="index" class="chat-source">
                <div class="flex items-start gap-2"><details class="min-w-0 flex-1">
                  <summary class="cursor-pointer break-all text-xs font-medium text-gray-700 dark:text-gray-200">{{ file.name }} · {{ t('chat.preview') }}</summary>
                  <p v-for="sheet in file.sheets" :key="sheet.name" class="mt-1 text-xs text-gray-500">{{ sheet.name }} · {{ t('chat.sheetSize', { rows: sheet.rows, columns: sheet.columns }) }}</p>
                  <pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words text-xs" data-testid="chat-file-preview">{{ file.content.slice(0, 3000) }}</pre>
                  <p v-if="file.content.length > 3000" class="mt-1 text-xs text-gray-500">{{ t('chat.previewPartial') }}</p>
                </details><button type="button" :disabled="busy || filesLoading" :aria-label="t('chat.removeFile', { name: file.name })" class="px-1 text-gray-400" @click="draftFiles = draftFiles.filter((_, i) => i !== index)">×</button></div>
              </div>
            </div>
            <textarea ref="composer" v-model="draft" :placeholder="t('chat.placeholder')" :aria-label="t('chat.placeholder')" class="max-h-48 min-h-20 w-full resize-y bg-transparent px-4 pt-4 text-sm leading-6 text-gray-900 outline-none dark:text-gray-100" rows="3" maxlength="100000" data-testid="chat-input" @keydown="onKeydown"></textarea>
            <div class="flex items-center justify-between gap-2 px-3 pb-3 pt-1">
              <div class="flex flex-wrap items-center gap-3">
                <button type="button" class="text-xs font-medium text-primary-600" :disabled="busy || filesLoading" data-testid="chat-attach" @click="fileInput?.click()">{{ filesLoading ? t('chat.readingFiles') : t('chat.attach') }}</button>
                <input ref="fileInput" type="file" class="sr-only" accept=".xlsx,.xls,.csv,.tsv,.txt,.md" multiple :aria-label="t('chat.attach')" :disabled="busy || filesLoading" data-testid="chat-file-input" @change="chooseFiles" />
                <span class="hidden text-xs text-gray-400 xl:block">{{ t('chat.keyboardHint') }}</span>
              </div>
              <button v-if="busy" type="button" class="btn btn-secondary text-sm" data-testid="chat-stop" @click="stop">■ {{ t('chat.stop') }}</button>
              <button v-else type="submit" class="btn btn-primary text-sm" :disabled="!canSend || !draft.trim()" data-testid="chat-send">{{ t('chat.send') }} ↑</button>
            </div>
          </form>
          <p class="mt-2 text-[11px] leading-5 text-gray-400">{{ t('chat.fileHint') }}</p>
          <p v-if="current?.financeMode" class="mt-1 text-[11px] leading-5 text-gray-400">{{ t('chat.reviewHint') }}</p>
          <p class="mt-2 text-center text-[11px] leading-5 text-gray-400">{{ t('chat.chargingHint') }} <span class="lg:hidden">{{ t('chat.historyHint') }}</span></p>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import * as keysAPI from '@/api/keys'
import { ChatError, listChatModels, loadChats, saveChats, streamChat, type ChatConversation, type ChatMessage } from '@/api/chat'
import type { ApiKey } from '@/types'
import { createRandomId } from '@/utils/id'
import { useClipboard } from '@/composables/useClipboard'
import { CHAT_MAX_FILES, CHAT_MAX_INPUT, chatMarkdown, chatTables, chatTranscript, downloadChatFile, readChatFile, tableTSV } from '@/utils/chatWorkspace'
import type { ChatAttachment } from '@/api/chat'

const { t } = useI18n(), auth = useAuthStore(), app = useAppStore()
type KeyChoice = Pick<ApiKey, 'id' | 'user_id' | 'name' | 'group_id' | 'group' | 'status' | 'quota' | 'quota_used' | 'expires_at'>
const conversations = ref<ChatConversation[]>([]), selectedId = ref(''), keys = ref<KeyChoice[]>([])
const models = ref<string[]>([]), busy = ref(false), error = ref(''), historySearch = ref('')
const keysLoading = ref(false), modelsLoading = ref(false), keysError = ref(false), modelsError = ref(false), storageError = ref(false)
const messagePane = ref<HTMLElement>(), followOutput = ref(true)
const current = computed(() => conversations.value.find(c => c.id === selectedId.value))
const draft = computed({ get: () => current.value?.draft || '', set: value => { if (current.value) current.value.draft = value } })
const draftFiles = computed({ get: () => current.value?.draftFiles || [], set: (value: ChatAttachment[]) => { if (current.value) current.value.draftFiles = value } })
const sortedChats = computed(() => [...conversations.value].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)))
const visibleChats = computed(() => sortedChats.value.filter(chat => chat.title.toLocaleLowerCase().includes(historySearch.value.toLocaleLowerCase())))
const composer = ref<HTMLTextAreaElement>(), fileInput = ref<HTMLInputElement>()
const filesLoading = ref(false), dragging = ref(false), exporting = ref(false)
const tasks = ['reconcile', 'variance', 'statement', 'report'] as const
const { copyToClipboard: copy } = useClipboard()
const canSend = computed(() => !!auth.user && !busy.value && !filesLoading.value && !keysLoading.value && !!current.value?.model.trim() && keys.value.some(k => k.id === current.value?.keyId))
let alive = true, generation = 0, modelsController: AbortController | undefined, runController: AbortController | undefined, filesController: AbortController | undefined
let draftTimer: ReturnType<typeof setTimeout> | undefined

function persist() {
  if (auth.user && alive) storageError.value = !saveChats(auth.user.id, conversations.value)
}
function newChat() {
  if (busy.value) return
  const chat: ChatConversation = { id: createRandomId(), title: '', keyId: current.value?.keyId ?? keys.value[0]?.id ?? null,
    model: current.value?.model ?? '', effort: current.value?.effort ?? '', financeMode: true,
    maxOutputTokens: current.value?.maxOutputTokens ?? 8192, draft: '', draftFiles: [], updatedAt: new Date().toISOString(), messages: [] }
  conversations.value.unshift(chat); selectedId.value = chat.id; error.value = ''; persist()
}
function deleteChat() {
  if (busy.value || !current.value || !window.confirm(t('chat.deleteConfirm'))) return
  conversations.value = conversations.value.filter(c => c.id !== selectedId.value)
  selectedId.value = sortedChats.value[0]?.id ?? ''
  if (!current.value) newChat()
  error.value = ''; persist()
}
const usable = (k: KeyChoice, userId: number) => k.user_id === userId && k.status === 'active' && !!k.group_id &&
  (!k.expires_at || Date.parse(k.expires_at) > Date.now()) && !(k.quota > 0 && k.quota_used >= k.quota)

async function loadKeys() {
  const userId = auth.user?.id, request = ++generation
  if (!userId) return
  keysLoading.value = true; keysError.value = false
  try {
    const all: KeyChoice[] = []
    for (let page = 1; ; page++) {
      const result = await keysAPI.list(page, 100, { status: 'active' })
      if (!alive || generation !== request || auth.user?.id !== userId) return
      all.push(...result.items.filter(k => usable(k, userId)).map(k => ({ id: k.id, user_id: k.user_id, name: k.name,
        group_id: k.group_id, group: k.group, status: k.status, quota: k.quota, quota_used: k.quota_used, expires_at: k.expires_at })))
      if (page >= result.pages || !result.items.length) break
    }
    keys.value = all
    if (current.value && !all.some(k => k.id === current.value?.keyId)) current.value.keyId = current.value.messages.length ? null : all[0]?.id ?? null
    else void loadModels()
  } catch { if (alive && generation === request) keysError.value = true }
  finally { if (alive && generation === request) keysLoading.value = false }
}
async function loadModels() {
  modelsController?.abort()
  const controller = new AbortController(); modelsController = controller
  const chat = current.value, userId = auth.user?.id, keyId = chat?.keyId
  models.value = []; modelsError.value = false; modelsLoading.value = false
  if (!chat || !userId || !keyId || !keys.value.some(k => k.id === keyId)) return
  modelsLoading.value = true
  const timer = setTimeout(() => controller.abort(), 20000)
  try {
    const key = await keysAPI.getById(keyId)
    if (controller.signal.aborted || !alive || auth.user?.id !== userId) return
    if (!usable(key, userId)) throw new Error('Unavailable key')
    const result = await listChatModels(key.key, controller.signal)
    if (controller.signal.aborted || !alive || current.value?.id !== chat.id || chat.keyId !== keyId || auth.user?.id !== userId) return
    models.value = result.filter(m => !/(?:image|dall-e|embedding|whisper|tts|video|sora)/i.test(m))
    if (!chat.model) chat.model = models.value.includes('gpt-6.1-sol') ? 'gpt-6.1-sol' : models.value[0] ?? ''
    persist()
  } catch { if (alive && modelsController === controller) modelsError.value = true }
  finally { clearTimeout(timer); if (alive && modelsController === controller) modelsLoading.value = false }
}
watch(() => [current.value?.id, current.value?.keyId], () => { error.value = ''; void loadModels() })
watch(selectedId, () => { filesController?.abort(); filesLoading.value = false; followOutput.value = true; scrollToEnd() })
watch(() => [current.value?.model, current.value?.effort, current.value?.financeMode, current.value?.maxOutputTokens], persist)
watch(() => [draft.value, draftFiles.value], () => { clearTimeout(draftTimer); draftTimer = setTimeout(persist, 400) }, { deep: true })
watch(() => auth.user?.id, () => {
  runController?.abort(); modelsController?.abort(); filesController?.abort(); generation++
  runController = undefined; busy.value = false; filesLoading.value = false; keys.value = []; models.value = []; error.value = ''; historySearch.value = ''
  conversations.value = auth.user ? loadChats(auth.user.id) : []; selectedId.value = conversations.value[0]?.id ?? ''
  if (auth.user) { if (!current.value) newChat(); void loadKeys() }
}, { immediate: true, flush: 'sync' })

async function send(retry = false) {
  if (!canSend.value || !current.value || !auth.user || (!retry && !draft.value.trim())) return
  if (!retry && draft.value.length + draftFiles.value.reduce((size, file) => size + file.content.length, 0) > CHAT_MAX_INPUT) {
    error.value = t('chat.files.content'); return
  }
  const chat = current.value, userId = auth.user.id, keyId = chat.keyId!, selection = keys.value.find(k => k.id === keyId)!
  const controller = new AbortController(); runController = controller
  busy.value = true; error.value = ''; followOutput.value = true
  let assistant: ChatMessage | undefined
  try {
    const key = await keysAPI.getById(keyId)
    if (controller.signal.aborted || !alive || auth.user?.id !== userId) return
    if (!usable(key, userId) || key.group_id !== selection.group_id || !key.key) { error.value = t('chat.keyChanged'); return }
    if (retry) {
      if (chat.messages.at(-1)?.role !== 'assistant') return
      chat.messages.pop()
    } else {
      const content = draft.value.trim(), attachments = [...draftFiles.value]; draft.value = ''; draftFiles.value = []
      chat.messages.push({ id: createRandomId(), role: 'user', content, attachments, reasoning: '', status: 'complete' })
      if (!chat.title) chat.title = content.slice(0, 36)
    }
    const request = { ...chat, messages: [...chat.messages] }
    chat.messages.push({ id: createRandomId(), role: 'assistant', content: '', reasoning: '', status: 'streaming' })
    assistant = chat.messages[chat.messages.length - 1]
    chat.updatedAt = new Date().toISOString(); persist(); scrollToEnd()
    const status = await streamChat(request, key.key, controller.signal, (content, reasoning) => {
      if (!alive || controller.signal.aborted || auth.user?.id !== userId) return
      assistant!.content += content; assistant!.reasoning += reasoning; scrollToEnd()
    })
    assistant.status = status
    if (!assistant.content.trim()) { assistant.status = 'failed'; error.value = t('chat.errors.empty') }
  } catch (cause) {
    if (assistant) assistant.status = controller.signal.aborted ? 'stopped' : 'failed'
    if (alive && auth.user?.id === userId && !controller.signal.aborted) {
      error.value = cause instanceof ChatError ? cause.code === 'http' ? t('chat.errors.http', { status: cause.status ?? '—' }) :
        t(cause.code === 'inputLimit' ? 'chat.errors.inputLimit' : cause.code === 'network' ? 'chat.errors.network' : 'chat.errors.stream') : t('chat.errors.network')
    }
  } finally {
    if (alive && runController === controller && auth.user?.id === userId) { busy.value = false; persist(); scrollToEnd() }
  }
}
function stop() { runController?.abort() }
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing && event.keyCode !== 229) {
    event.preventDefault(); void send(false)
  }
}
function onScroll() {
  const pane = messagePane.value
  if (pane) followOutput.value = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 90
}
function scrollToEnd() {
  void nextTick(() => { if (followOutput.value && messagePane.value) messagePane.value.scrollTop = messagePane.value.scrollHeight })
}
function useTask(task: typeof tasks[number]) {
  if (!current.value || busy.value) return
  if (draft.value.trim() && !window.confirm(t('chat.replaceDraft'))) return
  current.value.financeMode = true
  draft.value = t('chat.tasks.' + task + '.prompt')
  void nextTick(() => composer.value?.focus())
}
async function addFiles(files: File[]) {
  const chat = current.value, userId = auth.user?.id
  if (!chat || !userId || busy.value || filesLoading.value || !files.length) return
  if (draftFiles.value.length + files.length > CHAT_MAX_FILES) { error.value = t('chat.files.count'); return }
  const controller = new AbortController(); filesController = controller; filesLoading.value = true; error.value = ''
  try {
    // Accept the selected batch atomically so a failed file never silently disappears from the request.
    const parsed: ChatAttachment[] = []
    for (const file of files) parsed.push(await readChatFile(file, controller.signal))
    if (controller.signal.aborted || !alive || auth.user?.id !== userId || current.value?.id !== chat.id) return
    if ([...draftFiles.value, ...parsed].reduce((size, file) => size + file.content.length, draft.value.length) > CHAT_MAX_INPUT) throw new Error('content')
    draftFiles.value = [...draftFiles.value, ...parsed]; persist()
  } catch (cause) {
    if (!controller.signal.aborted && alive && auth.user?.id === userId && current.value?.id === chat.id) {
      const code = cause instanceof Error ? cause.message : 'parse'
      error.value = t('chat.files.' + (['size', 'unsupported', 'content', 'empty', 'sheets', 'rows', 'columns', 'cells', 'formula', 'timeout'].includes(code) ? code : 'parse'))
    }
  } finally { if (filesController === controller) filesLoading.value = false }
}
function chooseFiles(event: Event) {
  const input = event.target as HTMLInputElement
  void addFiles(Array.from(input.files || [])); input.value = ''
}
function dropFiles(event: DragEvent) { dragging.value = false; void addFiles(Array.from(event.dataTransfer?.files || [])) }
function exportConversation() {
  if (current.value) downloadChatFile(chatTranscript(current.value), (current.value.title || 'Chat') + '.md', 'text/markdown;charset=utf-8')
}
function hasTables(content: string) { return chatTables(content).length > 0 }
function copyTable(content: string) { const table = chatTables(content)[0]; if (table) void copy(tableTSV(table)) }
async function downloadTables(content: string) {
  if (exporting.value) return
  exporting.value = true
  try {
    const XLSX = await import('xlsx'), book = XLSX.utils.book_new()
    for (const [index, rows] of chatTables(content).entries()) {
      // aoa_to_sheet keeps strings as text cells, including leading zeroes and formula-like values.
      XLSX.utils.book_append_sheet(book, XLSX.utils.aoa_to_sheet(rows), 'Table ' + (index + 1))
    }
    const bytes: ArrayBuffer = XLSX.write(book, { type: 'array', bookType: 'xlsx' })
    downloadChatFile(bytes, (current.value?.title || 'Tables') + '.xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet')
  } catch { app.showError(t('chat.exportError')) }
  finally { exporting.value = false }
}
function downloadReport(content: string) {
  // A self-contained browser document; no model-authored scripts, links or external resources.
  const document = '<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src \'none\'; style-src \'unsafe-inline\'; base-uri \'none\'; form-action \'none\'"><title>Report</title><style>body{max-width:960px;margin:40px auto;padding:0 24px;font:15px/1.8 system-ui}table{border-collapse:collapse;max-width:100%}td,th{border:1px solid #bbb;padding:6px 12px}pre{white-space:pre-wrap} @media print{body{margin:0;max-width:none}}</style></head><body>' +
    chatMarkdown(content).replace(/<a\b[^>]*>([\s\S]*?)<\/a>/gi, '$1') + '</body></html>'
  downloadChatFile(document, (current.value?.title || 'Report') + '.html', 'text/html;charset=utf-8')
  app.showSuccess(t('chat.reportDownloaded'))
}
window.addEventListener('pagehide', persist)
onBeforeUnmount(() => {
  const last = current.value?.messages.at(-1)
  if (last?.status === 'streaming') last.status = 'stopped'
  clearTimeout(draftTimer); persist(); alive = false; generation++; runController?.abort(); modelsController?.abort(); filesController?.abort()
  window.removeEventListener('pagehide', persist)
})
</script>

<style scoped>
.chat-shell { @apply flex overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900; height: calc(100dvh - 10rem); min-height: 620px; }
.chat-history { @apply w-60 shrink-0 flex-col border-r border-gray-100 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-950/40; }
.chat-history-item { @apply w-full rounded-lg px-3 py-3 text-left text-sm text-gray-600 transition-colors hover:bg-gray-100 disabled:opacity-50 dark:text-gray-300 dark:hover:bg-dark-800; }
.chat-history-active { @apply bg-white font-medium text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300; }
.chat-toolbar { @apply border-b border-gray-100 px-4 py-4 dark:border-dark-700 md:px-6; }
.chat-field { @apply flex flex-col gap-1.5 text-xs font-medium text-gray-500; }
.chat-field .input { @apply text-sm font-normal; }
.chat-icon-button { @apply mb-0.5 flex h-10 w-8 shrink-0 items-center justify-center rounded-lg text-xl text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:opacity-40 dark:hover:bg-dark-800 dark:hover:text-gray-200; }
.chat-composer { @apply overflow-hidden rounded-2xl border border-gray-200 bg-gray-50/40 shadow-sm transition-shadow focus-within:border-primary-400 focus-within:ring-2 focus-within:ring-primary-100 dark:border-dark-600 dark:bg-dark-950/30 dark:focus-within:ring-primary-900/40; }
.chat-task { @apply rounded-xl border border-gray-200 bg-gray-50/50 p-4 text-left transition-colors hover:border-primary-300 hover:bg-primary-50/50 dark:border-dark-600 dark:bg-dark-800/40 dark:hover:border-primary-600; }
.chat-source { @apply rounded-lg border border-gray-200 bg-white/70 p-3 dark:border-dark-600 dark:bg-dark-800/60; }
.chat-markdown { @apply break-words text-sm leading-7 text-gray-800 dark:text-gray-100; }
.chat-markdown :deep(p + p), .chat-markdown :deep(pre), .chat-markdown :deep(ul), .chat-markdown :deep(ol), .chat-markdown :deep(blockquote) { margin-top: 0.8em; margin-bottom: 0.8em; }
.chat-markdown :deep(pre) { @apply overflow-x-auto rounded-xl bg-gray-900 p-4 text-xs leading-6 text-gray-100; }
.chat-markdown :deep(code) { @apply rounded bg-gray-100 px-1 py-0.5 font-mono text-xs dark:bg-dark-700; }
.chat-markdown :deep(pre code) { @apply bg-transparent p-0; }
.chat-markdown :deep(ul) { @apply list-disc pl-6; }
.chat-markdown :deep(ol) { @apply list-decimal pl-6; }
.chat-markdown :deep(a) { @apply text-primary-600 underline; }
.chat-markdown :deep(blockquote) { @apply border-l-2 border-primary-300 pl-4 text-gray-500; }
.chat-markdown :deep(h1), .chat-markdown :deep(h2), .chat-markdown :deep(h3) { @apply mb-2 mt-5 font-semibold; }
.chat-markdown :deep(h1) { @apply text-xl; }
.chat-markdown :deep(h2) { @apply text-lg; }
.chat-markdown :deep(table) { @apply my-3 block max-w-full overflow-x-auto border-collapse; }
.chat-markdown :deep(th), .chat-markdown :deep(td) { @apply border border-gray-200 px-3 py-2 text-left dark:border-dark-600; }
@media (max-width: 639px) { .chat-shell { height: calc(100dvh - 7rem); min-height: 560px; } }
</style>
