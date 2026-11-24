import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { isCancel } from 'axios'

const { get, post, put, remove } = vi.hoisted(() => ({
  get: vi.fn(), post: vi.fn(), put: vi.fn(), remove: vi.fn()
}))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put, delete: remove } }))

import * as groups from '@/api/admin/groups'
import * as proxies from '@/api/admin/proxies'
import { clearAdminConfigCache } from '@/api/admin/configCache'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

describe('admin selector configuration cache', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-24T00:00:00Z'))
    localStorage.clear()
    localStorage.setItem('auth_token', 'session-a')
    clearAdminConfigCache()
    vi.resetAllMocks()
    get.mockResolvedValue({ data: [{ id: 1, name: 'original', nested: { value: 1 } }] })
    post.mockResolvedValue({ data: {} })
    put.mockResolvedValue({ data: {} })
    remove.mockResolvedValue({ data: {} })
  })

  afterEach(() => {
    clearAdminConfigCache()
    localStorage.clear()
    vi.useRealTimers()
  })

  it('merges concurrent reads, isolates consumer edits, and expires after 30 seconds', async () => {
    const rows = await Promise.all(Array.from({ length: 6 }, () => groups.getAll()))
    expect(get).toHaveBeenCalledTimes(1)
    rows[0]![0]!.name = 'edited locally'
    expect(rows[1]![0]!.name).toBe('original')
    expect((await groups.getAll())[0]!.name).toBe('original')
    vi.advanceTimersByTime(29_999)
    await groups.getAll()
    expect(get).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    await groups.getAll()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('separates platform, inactive and proxy selectors; counts and monitoring stay fresh', async () => {
    for (let repeat = 0; repeat < 2; repeat++) {
      await groups.getAll()
      await groups.getAll('openai')
      await groups.getAllIncludingInactive()
      await proxies.getAll()
      await proxies.getAllWithCount()
      await groups.getCapacitySummary()
      await groups.getUsageSummary()
    }
    expect(get).toHaveBeenCalledTimes(10) // four selectors + six live reads
    expect(get).toHaveBeenCalledWith('/admin/groups/all', { params: { platform: 'openai' } })
    expect(get).toHaveBeenCalledWith('/admin/groups/all', { params: { include_inactive: true } })
  })

  it.each([
    ['group update', () => groups.update(1, { name: 'changed' }), 'groups'],
    ['group create', () => groups.create({} as Parameters<typeof groups.create>[0]), 'groups'],
    ['group duplicate', () => groups.duplicate(1), 'groups'],
    ['group delete', () => groups.deleteGroup(1), 'groups'],
    ['group order', () => groups.updateSortOrder([{ id: 1, sort_order: 2 }]), 'groups'],
    ['composite route delete', () => groups.deleteCompositeRoute(1, 2), 'groups'],
    ['proxy update', () => proxies.update(1, { name: 'changed' }), 'proxies'],
    ['proxy create', () => proxies.create({} as Parameters<typeof proxies.create>[0]), 'proxies'],
    ['proxy delete', () => proxies.deleteProxy(1), 'proxies'],
    ['proxy batch create', () => proxies.batchCreate([]), 'proxies'],
    ['proxy batch delete', () => proxies.batchDelete([1]), 'proxies'],
    ['proxy import', () => proxies.importData({} as Parameters<typeof proxies.importData>[0]), 'proxies'],
    ['proxy connectivity result', () => proxies.testProxy(1), 'proxies'],
    ['proxy quality result', () => proxies.checkProxyQuality(1), 'proxies'],
  ] as const)('%s invalidates only its resource after a successful save', async (_name, save, resource) => {
    await groups.getAll()
    await groups.getAll('openai')
    await proxies.getAll()
    await save()
    await groups.getAll()
    await groups.getAll('openai')
    await proxies.getAll()
    expect(get).toHaveBeenCalledTimes(resource === 'groups' ? 5 : 4)
  })

  it('does not cache request failures or malformed successful responses', async () => {
    get.mockRejectedValueOnce(new Error('network'))
    await expect(proxies.getAll()).rejects.toThrow('network')
    get.mockResolvedValueOnce({ data: null })
    await expect(proxies.getAll()).rejects.toThrow('Invalid proxy list response')
    get.mockResolvedValueOnce({ data: {} })
    await expect(groups.getAll()).rejects.toThrow('Invalid group list response')
    expect(await proxies.getAll()).toHaveLength(1)
    expect(await groups.getAll()).toHaveLength(1)
    expect(get).toHaveBeenCalledTimes(5)
  })

  it.each(['logout', 'relogin', 'same-token session', 'other tab'] as const)(
    'discards old in-flight data on %s', async (boundary) => {
      const old = deferred<{ data: { id: number }[] }>()
      get.mockReturnValueOnce(old.promise)
      const pending = groups.getAll().catch((error: unknown) => error)
      await Promise.resolve()
      if (boundary === 'logout') localStorage.removeItem('auth_token')
      if (boundary === 'relogin') localStorage.setItem('auth_token', 'session-b')
      if (boundary === 'same-token session') clearAdminConfigCache()
      if (boundary === 'other tab') {
        window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token' }))
      }
      old.resolve({ data: [{ id: 99 }] })
      expect(isCancel(await pending)).toBe(true)
      expect((await groups.getAll())[0]!.id).toBe(1)
      expect(get).toHaveBeenCalledTimes(2)
    },
  )

  it('reloads a read that completed after a successful write', async () => {
    const old = deferred<{ data: { id: number }[] }>()
    get.mockReturnValueOnce(old.promise)
    const pending = proxies.getAll()
    await Promise.resolve()
    await proxies.update(1, { name: 'updated' })
    old.resolve({ data: [{ id: 99 }] })
    expect((await pending)[0]!.id).toBe(1)
    expect(get).toHaveBeenCalledTimes(2)
    await proxies.getAll()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('does not merge unauthenticated reads', async () => {
    localStorage.removeItem('auth_token')
    await Promise.all([proxies.getAll(), proxies.getAll()])
    expect(get).toHaveBeenCalledTimes(2)
  })
})
