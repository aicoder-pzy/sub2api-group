import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ChatSync, mergeChats } from '../chatSync'
import { chatSchema, type ChatConversation } from '../chat'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../client', () => ({ apiClient: mocks }))
type Record = { id: string; revision: number; deleted: boolean; conversation?: ChatConversation }
let records: Map<string, Record>, clients: ChatSync[]
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value))
const chat = (): ChatConversation => ({ id: 'shared', title: 'Reconcile', keyId: 4, model: 'vision-model', effort: '', updatedAt: '', messages: [], draft: '', maxOutputTokens: 4096 })
const image = { name: 'receipt.png', dataUrl: 'data:image/png;base64,AA==', width: 30, height: 40 }
function device(chats: ChatConversation[]) {
  const status = vi.fn(), conflict = vi.fn(() => ' (conflict copy)'), changed = vi.fn()
  const sync = new ChatSync({ chats: () => chats, changed, status, conflict, paused: () => false })
  clients.push(sync)
  return { sync, chats, status, conflict, changed }
}
beforeEach(() => {
  records = new Map(); clients = []; vi.clearAllMocks()
  mocks.get.mockImplementation(async (url: string) => {
    if (url === '/chat/conversations') return { data: [...records.values()].map(({ conversation: _, ...info }) => clone(info)) }
    const record = records.get(url.split('/').at(-1)!)
    if (!record) throw { status: 404 }
    return { data: clone(record) }
  })
  mocks.put.mockImplementation(async (url: string, input) => {
    const id = url.split('/').at(-1)!, existing = records.get(id)
    if (input.revision !== (existing?.revision || 0) || existing?.deleted) throw { status: 409 }
    const saved = { id, revision: (existing?.revision || 0) + 1, deleted: input.deleted === true, conversation: input.conversation && clone(input.conversation) }
    records.set(id, saved); return { data: clone(saved) }
  })
})
afterEach(() => { clients.forEach(c => c.dispose()) })

describe('same-account conversation sync', () => {
  it('transfers phone photos into an independent desktop draft without an inference request', async () => {
    const desktop = device([chat()]); await desktop.sync.sync()
    const phone = device([]); await phone.sync.sync()
    desktop.chats[0].draft = 'Compare this receipt'; desktop.sync.changed()
    phone.chats[0].draftImages = [image]; phone.sync.changed(); await phone.sync.sync()
    await desktop.sync.sync(); await phone.sync.sync()
    expect(desktop.chats[0].draft).toBe('Compare this receipt')
    expect(desktop.chats[0].draftImages).toEqual([image])
    expect(phone.chats[0].draft).toBe('Compare this receipt')
    expect(desktop.conflict).not.toHaveBeenCalled()
    const writes = mocks.put.mock.calls.length, reads = mocks.get.mock.calls.length
    await desktop.sync.sync()
    expect(mocks.put).toHaveBeenCalledTimes(writes)
    expect(mocks.get).toHaveBeenCalledTimes(reads + 1) // unchanged index never downloads image payloads
    expect(mocks.put.mock.calls.every(([url]) => url.startsWith('/chat/conversations/'))).toBe(true)
  })
  it('retains conflicting text in a new conversation and never overwrites the other device', async () => {
    const a = device([chat()]); await a.sync.sync()
    const b = device([]); await b.sync.sync()
    a.chats[0].draft = 'Desktop version'; a.sync.changed()
    b.chats[0].draft = 'Phone version'; b.sync.changed(); await b.sync.sync()
    await a.sync.sync()
    expect(a.conflict).toHaveBeenCalledOnce()
    expect(a.chats.find(c => c.id === 'shared')?.draft).toBe('Phone version')
    expect(a.chats.find(c => c.id !== 'shared')?.draft).toBe('Desktop version')
    expect(records.get('shared')?.conversation?.draft).toBe('Phone version')
  })
  it('propagates deletion and retains new offline edits as a separate copy', async () => {
    const a = device([chat()]); await a.sync.sync()
    const b = device([]); await b.sync.sync()
    const stale = device(clone(b.chats))
    b.chats[0].draftImages = [image]; b.sync.changed()
    expect(await a.sync.remove(a.chats[0])).toBe(true); a.chats.splice(0)
    await stale.sync.sync(); expect(stale.chats).toEqual([])
    await b.sync.sync()
    expect(b.chats).toHaveLength(1)
    expect(b.chats[0].id).not.toBe('shared')
    expect(b.chats[0].draftImages).toEqual([image])
    expect(records.get('shared')?.deleted).toBe(true)
  })
  it('preserves edits made during upload and retries without duplicating a committed conversation', async () => {
    const a = device([chat()])
    const put = mocks.put.getMockImplementation()!
    mocks.put.mockImplementationOnce(async (...args) => {
      const result = await put(...args)
      a.chats[0].draft = 'Typed during upload'
      return result
    })
    await a.sync.sync(); expect(a.chats[0].cloudSynced).toBe(false)
    await a.sync.sync(); expect(records.get('shared')?.conversation?.draft).toBe('Typed during upload')
    a.chats[0].draft = 'Committed before disconnect'; a.sync.changed()
    mocks.put.mockImplementationOnce(async (...args) => { await put(...args); throw { status: 0 } })
    await a.sync.sync(); await a.sync.sync()
    expect(a.chats).toHaveLength(1); expect(a.chats[0].cloudSynced).toBe(true)
  })
  it('keeps local data on quota/network failures and ignores responses after account disposal', async () => {
    const a = device([chat()]); mocks.put.mockRejectedValueOnce({ status: 413 })
    await a.sync.sync(); expect(a.status).toHaveBeenLastCalledWith('limit'); expect(a.chats).toHaveLength(1)
    let resolve!: (value: unknown) => void
    mocks.get.mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const pending = a.sync.sync(); a.sync.dispose()
    resolve({ data: [{ id: 'other-account', revision: 1, deleted: false }] }); await pending
    expect(a.chats.map(c => c.id)).toEqual(['shared'])
  })
  it('merges consumed photos and new photos without restoring the consumed draft', () => {
    const base = { ...chat(), draftImages: [image] }, local = { ...chat(), draftImages: [] }
    const next = { ...image, name: 'new.png' }, remote = { ...chat(), draftImages: [image, next] }
    expect(mergeChats(base, local, remote)?.draftImages).toEqual([next])
    expect(JSON.stringify(chatSchema({ ...base, cloudRevision: 99, cloudSynced: true, key: 'SECRET' } as ChatConversation))).not.toMatch(/SECRET|cloudRevision/)
  })
})
