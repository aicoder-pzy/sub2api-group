import { apiClient } from './client'
import { chatSchema, type ChatConversation } from './chat'
import { createRandomId } from '@/utils/id'

export type ChatSyncStatus = 'syncing' | 'synced' | 'offline' | 'limit'
interface RecordInfo { id: string; revision: number; deleted: boolean; conversation?: ChatConversation }
const endpoint = '/chat/conversations'
const equal = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)
const snapshot = (chat: ChatConversation) => JSON.stringify(chatSchema(chat))
const clone = (chat: ChatConversation): ChatConversation => JSON.parse(snapshot(chat))

/** Merge different fields and independent attachment additions; conflicting edits retain a separate copy. */
export function mergeChats(base: ChatConversation | undefined, local: ChatConversation, remote: ChatConversation): ChatConversation | undefined {
  const l = chatSchema(local), r = chatSchema(remote), b = base && chatSchema(base)
  if (equal(l, r)) return r
  if (!b) return undefined
  const merged = { ...r }
  for (const key of Object.keys(l) as (keyof ChatConversation)[]) {
    if (key === 'updatedAt') continue
    if (equal(l[key], b[key]) || equal(l[key], r[key])) continue
    if (equal(r[key], b[key])) { Object.assign(merged, { [key]: l[key] }); continue }
    if (key === 'draftImages' || key === 'draftFiles') {
      const before = b[key] || [], left = l[key] || [], right = r[key] || []
      const combined = [...left.filter(item => !before.some(old => equal(old, item)) || right.some(other => equal(other, item))),
        ...right.filter(item => !before.some(old => equal(old, item)) && !left.some(other => equal(other, item)))]
      Object.assign(merged, { [key]: combined }); continue
    }
    return undefined
  }
  if ((merged.draftFiles?.length || 0) + (merged.draftImages?.length || 0) > 4) return undefined
  merged.updatedAt = [local.updatedAt, remote.updatedAt].sort().at(-1) || ''
  return merged
}

interface SyncHooks {
  chats: () => ChatConversation[]
  changed: () => void
  status: (status: ChatSyncStatus) => void
  conflict: () => string
  paused: () => boolean
}

/** One request at a time per page. CAS prevents another device's revision being overwritten. */
export class ChatSync {
  private controller = new AbortController()
  private bases = new Map<string, ChatConversation>()
  private running: Promise<void> | undefined
  private timer: ReturnType<typeof setTimeout> | undefined
  private deleting = new Set<string>()
  constructor(private hooks: SyncHooks) {
    for (const chat of hooks.chats()) if (chat.cloudSynced) this.bases.set(chat.id, clone(chat))
  }
  dispose() { this.controller.abort(); clearTimeout(this.timer) }
  changed() {
    let dirty = false
    for (const chat of this.hooks.chats()) {
      const base = this.bases.get(chat.id)
      if ((base && snapshot(chat) !== snapshot(base)) || (!base && !chat.cloudRevision)) chat.cloudSynced = false
      if (!chat.cloudSynced) dirty = true
    }
    if (dirty) this.hooks.status('syncing')
  }
  schedule() {
    clearTimeout(this.timer)
    this.timer = setTimeout(() => { void this.sync() }, 1200)
  }
  sync(): Promise<void> {
    if (this.controller.signal.aborted || this.hooks.paused()) return Promise.resolve()
    if (this.running) return this.running
    this.running = this.round().catch(cause => {
      if (!this.controller.signal.aborted) this.hooks.status((cause as { status?: number }).status === 413 ? 'limit' : 'offline')
    }).finally(() => { this.running = undefined })
    return this.running
  }
  private copyConflict(local: ChatConversation) {
    const copy = clone(local)
    copy.id = createRandomId(); copy.title = (copy.title.slice(0, 55) + this.hooks.conflict()).slice(0, 80)
    copy.updatedAt = new Date().toISOString(); copy.cloudSynced = false
    this.hooks.chats().unshift(copy)
  }
  private async round() {
    const signal = this.controller.signal
    this.hooks.status('syncing')
    const { data: index } = await apiClient.get<RecordInfo[]>(endpoint, { signal })
    if (signal.aborted || this.hooks.paused()) return
    const remoteIds = new Set(index.map(r => r.id))
    for (const info of index) {
      if (signal.aborted || this.hooks.paused()) return
      if (this.deleting.has(info.id)) continue
      let local = this.hooks.chats().find(c => c.id === info.id)
      const base = this.bases.get(info.id)
      if (info.deleted) {
        if (local) {
          if (!local.cloudSynced && (!base || snapshot(local) !== snapshot(base))) this.copyConflict(local)
          this.hooks.chats().splice(this.hooks.chats().indexOf(local), 1)
          this.bases.delete(info.id); this.hooks.changed()
        }
        continue
      }
      if (local?.cloudRevision === info.revision && base) continue
      const { data: record } = await apiClient.get<RecordInfo>(endpoint + '/' + encodeURIComponent(info.id), { signal })
      if (signal.aborted || this.hooks.paused()) return
      if (record.deleted || !record.conversation) { this.schedule(); continue }
      local = this.hooks.chats().find(c => c.id === info.id)
      const remote = chatSchema(record.conversation)
      const dirty = local && (!local.cloudSynced || (base && snapshot(local) !== snapshot(base)))
      let merged = remote
      if (local && dirty) {
        if (local.cloudRevision === record.revision) merged = clone(local)
        else {
          const result = mergeChats(base, local, remote)
          if (result) merged = result
          else this.copyConflict(local)
        }
      }
      this.bases.set(info.id, clone(remote))
      const value = { ...merged, cloudRevision: record.revision, cloudSynced: equal(merged, remote) }
      if (local) Object.assign(local, value)
      else this.hooks.chats().push(value)
      this.hooks.changed()
    }
    // Include legacy local conversations and copies retained after a concurrent edit.
    for (const local of [...this.hooks.chats()]) {
      if (signal.aborted || this.hooks.paused()) return
      if (this.deleting.has(local.id)) continue
      const base = this.bases.get(local.id)
      if (remoteIds.has(local.id) && base && snapshot(local) === snapshot(base)) {
        local.cloudSynced = true; continue
      }
      const sent = clone(local)
      try {
        const { data: saved } = await apiClient.put<RecordInfo>(endpoint + '/' + encodeURIComponent(local.id), {
          revision: local.cloudRevision || 0, conversation: sent
        }, { signal })
        if (signal.aborted) return
        this.bases.set(local.id, sent)
        local.cloudRevision = saved.revision; local.cloudSynced = snapshot(local) === snapshot(sent)
        this.hooks.changed()
      } catch (cause) {
        if ((cause as { status?: number }).status === 409) { this.schedule(); this.hooks.status('syncing'); return }
        throw cause
      }
    }
    this.hooks.status(this.hooks.chats().every(c => c.cloudSynced) ? 'synced' : 'syncing')
  }
  async remove(chat: ChatConversation): Promise<boolean> {
    this.deleting.add(chat.id)
    try {
      await this.running
      const signal = this.controller.signal
      const { data: record } = await apiClient.get<RecordInfo>(endpoint + '/' + encodeURIComponent(chat.id), { signal })
      if (signal.aborted) return false
      if (!record.deleted && record.revision !== chat.cloudRevision) { this.schedule(); return false }
      if (!record.deleted) await apiClient.put(endpoint + '/' + encodeURIComponent(chat.id), { revision: record.revision, deleted: true }, { signal })
      if (signal.aborted) return false
      this.bases.delete(chat.id)
      return true
    } catch (cause) {
      if (!this.controller.signal.aborted) this.hooks.status('offline')
      // A never-uploaded local conversation has no server record to delete.
      return !chat.cloudRevision && (cause as { status?: number }).status === 404
    } finally { this.deleting.delete(chat.id) }
  }
}
