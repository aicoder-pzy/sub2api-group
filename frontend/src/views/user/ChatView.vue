<template>
  <AppLayout>
    <div class="chat-shell">
      <aside class="chat-history hidden lg:flex">
        <div class="flex items-center gap-2 px-2 py-3 text-sm font-semibold text-gray-700 dark:text-gray-200">
          <Icon name="chat" size="md" class="text-primary-500" aria-hidden="true" />
          {{ t('chat.title') }}
        </div>
        <button class="btn btn-primary w-full" :disabled="busy" @click="newChat">+ {{ t('chat.newChat') }}</button>
        <h2 class="mb-2 mt-7 px-2 text-xs font-medium text-gray-400">{{ t('chat.conversations') }}</h2>
        <nav class="min-h-0 flex-1 space-y-1 overflow-y-auto" :aria-label="t('chat.conversations')">
          <button v-for="chat in sortedChats" :key="chat.id" class="chat-history-item" :class="{ 'chat-history-active': selectedId === chat.id }" :disabled="busy" :aria-current="selectedId === chat.id ? 'page' : undefined" @click="selectedId = chat.id">
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
          <div v-if="current" class="flex flex-wrap items-end gap-3">
            <label class="chat-field min-w-0 flex-1 basis-[calc(50%-0.75rem)] sm:basis-0"><span>{{ t('chat.key') }}</span>
              <select v-model="current.keyId" class="input" data-testid="chat-key" :disabled="busy || keysLoading" @change="current.model = ''; current.effort = ''">
                <option :value="null">{{ keysLoading ? t('chat.loading') : t('chat.selectKey') }}</option>
                <option v-for="key in keys" :key="key.id" :value="key.id">{{ key.group?.name || '#' + key.group_id }} · {{ key.name }}</option>
              </select>
            </label>
            <label class="chat-field min-w-0 flex-1 basis-[calc(50%-0.75rem)] sm:basis-0"><span>{{ t('chat.model') }}</span>
              <input v-model.trim="current.model" class="input" list="chat-models" :placeholder="modelsLoading ? t('chat.loading') : t('chat.selectModel')" :disabled="busy || !current.keyId" maxlength="200" autocomplete="off" data-testid="chat-model" />
              <datalist id="chat-models"><option v-for="model in models" :key="model" :value="model" /></datalist>
            </label>
            <label class="chat-field w-32"><span>{{ t('chat.effort') }}</span>
              <select v-model="current.effort" class="input" :disabled="busy"><option value="">{{ t('chat.default') }}</option><option v-for="effort in ['low', 'medium', 'high', 'xhigh']" :key="effort" :value="effort">{{ effort }}</option></select>
            </label>
            <button class="chat-icon-button" :disabled="busy || keysLoading" :title="t('chat.reload')" :aria-label="t('chat.reload')" @click="loadKeys">↻</button>
            <button class="chat-icon-button" :disabled="busy" :title="t('chat.deleteChat')" :aria-label="t('chat.deleteChat')" @click="deleteChat">×</button>
          </div>
          <p v-if="!keysLoading && !keys.length && !keysError" class="mt-3 text-sm text-gray-500">{{ t('chat.noKeys') }} <RouterLink to="/keys" class="text-primary-600 underline">{{ t('chat.manageKeys') }}</RouterLink></p>
          <p v-if="keysError || modelsError" role="alert" class="mt-3 text-sm text-amber-600">{{ t(keysError ? 'chat.keysError' : 'chat.modelsError') }}</p>
        </header>

        <div ref="messagePane" class="min-h-0 flex-1 overflow-y-auto scroll-smooth" role="log" :aria-label="t('chat.title')" :aria-busy="busy" @scroll="onScroll">
          <div v-if="!current?.messages.length" class="flex h-full min-h-64 flex-col items-center justify-center px-6 py-12 text-center">
            <div class="mb-6 flex h-14 w-14 items-center justify-center rounded-2xl bg-primary-50 text-2xl text-primary-600 dark:bg-primary-900/30">✦</div>
            <h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('chat.emptyTitle') }}</h1>
            <p class="mt-3 text-sm text-gray-500">{{ t('chat.emptyHint') }}</p>
          </div>
          <div v-else class="mx-auto max-w-4xl space-y-7 px-4 py-8 md:px-8">
            <article v-for="message in current.messages" :key="message.id" :class="message.role === 'user' ? 'ml-auto max-w-[90%]' : 'mr-auto w-full'">
              <div class="mb-2 text-xs font-medium text-gray-400" :class="{ 'text-right': message.role === 'user' }">{{ t(message.role === 'user' ? 'chat.you' : 'chat.assistant') }}</div>
              <p v-if="message.role === 'user'" class="ml-auto w-fit whitespace-pre-wrap break-words rounded-2xl rounded-tr-sm bg-primary-50 px-5 py-3 text-sm leading-7 text-gray-800 dark:bg-primary-900/25 dark:text-gray-100">{{ message.content }}</p>
              <template v-else>
                <details v-if="message.reasoning" class="mb-3 rounded-xl border border-gray-100 px-4 py-3 dark:border-dark-700"><summary class="cursor-pointer text-xs text-gray-500">{{ t('chat.thinking') }}</summary><p class="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-gray-500">{{ message.reasoning }}</p></details>
                <!-- Only sanitized model Markdown is rendered as HTML. -->
                <div v-if="message.content" class="chat-markdown" v-html="renderMarkdown(message.content)"></div>
                <p v-if="message.status === 'streaming' && !message.content" class="animate-pulse py-2 text-sm text-gray-400" role="status">{{ t('chat.waiting') }}</p>
                <p v-if="['stopped', 'failed', 'limited'].includes(message.status)" class="mt-3 text-xs text-amber-600">{{ t('chat.' + message.status) }}</p>
                <button v-if="message.content && message.status !== 'streaming'" class="mt-3 text-xs text-gray-400 hover:text-primary-600" :aria-label="t('chat.copy')" @click="copy(message.content)">{{ t('chat.copy') }}</button>
              </template>
            </article>
          </div>
        </div>

        <div class="mx-auto w-full max-w-4xl px-4 pb-4 pt-2 md:px-8">
          <p v-if="error" role="alert" class="mb-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
          <p v-if="storageError" role="status" class="mb-2 text-xs text-amber-600">{{ t('chat.storageError') }}</p>
          <button v-if="current?.messages.at(-1)?.role === 'assistant' && !busy" class="mb-3 text-xs font-medium text-gray-500 hover:text-primary-600" :disabled="!canSend" data-testid="chat-retry" @click="send(true)">↻ {{ t('chat.retry') }}</button>
          <form class="chat-composer" @submit.prevent="send(false)">
            <textarea v-model="draft" :placeholder="t('chat.placeholder')" :aria-label="t('chat.placeholder')" class="max-h-48 min-h-20 w-full resize-y bg-transparent px-4 pt-4 text-sm leading-6 text-gray-900 outline-none dark:text-gray-100" rows="3" maxlength="100000" data-testid="chat-input" @keydown="onKeydown"></textarea>
            <div class="flex items-center justify-between gap-2 px-3 pb-3 pt-1">
              <span class="hidden text-xs text-gray-400 sm:block">{{ t('chat.keyboardHint') }}</span><span class="sm:hidden"></span>
              <button v-if="busy" type="button" class="btn btn-secondary text-sm" data-testid="chat-stop" @click="stop">■ {{ t('chat.stop') }}</button>
              <button v-else type="submit" class="btn btn-primary text-sm" :disabled="!canSend || !draft.trim()" data-testid="chat-send">{{ t('chat.send') }} ↑</button>
            </div>
          </form>
          <p class="mt-2 text-center text-[11px] leading-5 text-gray-400">{{ t('chat.chargingHint') }} <span class="lg:hidden">{{ t('chat.historyHint') }}</span></p>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import * as keysAPI from '@/api/keys'
import { ChatError, listChatModels, loadChats, saveChats, streamChat, type ChatConversation, type ChatMessage } from '@/api/chat'
import type { ApiKey } from '@/types'
import { createRandomId } from '@/utils/id'

const { t } = useI18n(), auth = useAuthStore(), app = useAppStore()
type KeyChoice = Pick<ApiKey, 'id' | 'user_id' | 'name' | 'group_id' | 'group' | 'status' | 'quota' | 'quota_used' | 'expires_at'>
const conversations = ref<ChatConversation[]>([]), selectedId = ref(''), keys = ref<KeyChoice[]>([])
const models = ref<string[]>([]), draft = ref(''), busy = ref(false), error = ref('')
const keysLoading = ref(false), modelsLoading = ref(false), keysError = ref(false), modelsError = ref(false), storageError = ref(false)
const messagePane = ref<HTMLElement>(), followOutput = ref(true)
const current = computed(() => conversations.value.find(c => c.id === selectedId.value))
const sortedChats = computed(() => [...conversations.value].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)))
const canSend = computed(() => !!auth.user && !busy.value && !keysLoading.value && !!current.value?.model.trim() && keys.value.some(k => k.id === current.value?.keyId))
let alive = true, generation = 0, modelsController: AbortController | undefined, runController: AbortController | undefined

function persist() {
  if (auth.user && alive) storageError.value = !saveChats(auth.user.id, conversations.value)
}
function newChat() {
  if (busy.value) return
  const chat: ChatConversation = { id: createRandomId(), title: '', keyId: current.value?.keyId ?? keys.value[0]?.id ?? null,
    model: current.value?.model ?? '', effort: current.value?.effort ?? '', updatedAt: new Date().toISOString(), messages: [] }
  conversations.value.unshift(chat); selectedId.value = chat.id; draft.value = ''; error.value = ''; persist()
}
function deleteChat() {
  if (busy.value || !current.value || !window.confirm(t('chat.deleteConfirm'))) return
  conversations.value = conversations.value.filter(c => c.id !== selectedId.value)
  selectedId.value = sortedChats.value[0]?.id ?? ''
  if (!current.value) newChat()
  error.value = ''; draft.value = ''; persist()
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
watch(selectedId, () => { draft.value = ''; followOutput.value = true; scrollToEnd() })
watch(() => [current.value?.model, current.value?.effort], persist)
watch(() => auth.user?.id, () => {
  runController?.abort(); modelsController?.abort(); generation++
  runController = undefined; busy.value = false; keys.value = []; models.value = []; error.value = ''; draft.value = ''
  conversations.value = auth.user ? loadChats(auth.user.id) : []; selectedId.value = conversations.value[0]?.id ?? ''
  if (auth.user) { if (!current.value) newChat(); void loadKeys() }
}, { immediate: true, flush: 'sync' })

async function send(retry = false) {
  if (!canSend.value || !current.value || !auth.user || (!retry && !draft.value.trim())) return
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
      const content = draft.value.trim(); draft.value = ''
      chat.messages.push({ id: createRandomId(), role: 'user', content, reasoning: '', status: 'complete' })
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
        t(cause.code === 'network' ? 'chat.errors.network' : 'chat.errors.stream') : t('chat.errors.network')
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
function renderMarkdown(text: string) {
  return DOMPurify.sanitize(marked.parse(text, { async: false, gfm: true, breaks: true }), {
    FORBID_TAGS: ['img', 'video', 'audio', 'iframe', 'style', 'form', 'input'], FORBID_ATTR: ['style']
  })
}
async function copy(text: string) {
  try { await navigator.clipboard.writeText(text); app.showSuccess(t('common.copied')) }
  catch { app.showError(t('common.copyFailed')) }
}
window.addEventListener('pagehide', persist)
onBeforeUnmount(() => {
  const last = current.value?.messages.at(-1)
  if (last?.status === 'streaming') last.status = 'stopped'
  persist(); alive = false; generation++; runController?.abort(); modelsController?.abort()
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
