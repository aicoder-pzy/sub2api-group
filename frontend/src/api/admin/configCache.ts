import { CanceledError } from 'axios'

// Only selector configuration belongs here; monitoring, counts and usage stay live.
const ttl = 30_000
const entries = new Map<string, { promise: Promise<unknown>; expiresAt: number }>()
let authToken: string | null = null
let session = 0

export function clearAdminConfigCache(): void {
  entries.clear()
  authToken = localStorage.getItem('auth_token')
  session++
}

export function invalidateAdminConfig(resource: 'groups' | 'proxies'): void {
  for (const key of entries.keys()) {
    if (key.startsWith(`${resource}:`)) entries.delete(key)
  }
}

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key === 'auth_token' || event.key === null) clearAdminConfigCache()
  })
}

export async function loadAdminConfig<T>(key: string, load: () => Promise<T>): Promise<T> {
  const token = localStorage.getItem('auth_token')
  if (token !== authToken) clearAdminConfigCache()
  if (!token) return load()
  const currentSession = session
  let entry = entries.get(key)
  if (!entry || entry.expiresAt <= Date.now()) {
    entry = { promise: Promise.resolve().then(load), expiresAt: Infinity }
    entries.set(key, entry)
    const pending = entry
    pending.promise = pending.promise.then((data) => {
      pending.expiresAt = Date.now() + ttl
      // Keep a private copy so components cannot mutate another selector's rows.
      return structuredClone(data)
    }).catch((error) => {
      if (entries.get(key) === pending) entries.delete(key)
      throw error
    })
  }
  const data = await entry.promise as T
  if (session !== currentSession || localStorage.getItem('auth_token') !== token) {
    throw new CanceledError('Admin configuration session changed')
  }
  // A write raced this read: return fresh configuration rather than the old reply.
  if (entries.get(key) !== entry) return loadAdminConfig(key, load)
  return structuredClone(data)
}
