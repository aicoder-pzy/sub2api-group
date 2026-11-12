import { apiClient } from '../client'

export interface SiteAmount {
  currency: string
  remaining: number | null
  limit?: number
  used?: number
  unlimited: boolean
}
export interface SiteKey {
  id: string
  name: string
  group_id: string
  group_name: string
  available: boolean
  status: string
  suffix?: string
  quota?: SiteAmount
}
export interface SiteGroup { id: string; name: string; description?: string; multiplier?: number }
export interface SiteKeyGroups { key: SiteKey; groups: SiteGroup[] }
export interface SiteInventory {
  revision: number
  status: 'ok' | 'attention'
  attempt_at: string
  received_at?: string
  wallet?: SiteAmount
  wallet_at?: string
  groups: SiteGroup[]
  keys: SiteKey[]
  errors: Record<string, string>
}
export interface UpstreamSite {
  id: string
  name: string
  kind: 'sub2api' | 'newapi'
  base_url: string
  inference_url: string
  purchase_note: string
  user_id: number
  proxy_id: number | null
  revision: number
  account_count: number
  can_refresh?: boolean
  inventory?: SiteInventory
  catalogs?: Record<string, { models: string[]; attempt_at: string }>
}
export type SiteForm = Pick<UpstreamSite, 'name' | 'kind' | 'base_url' | 'inference_url' | 'purchase_note' | 'user_id' | 'proxy_id' | 'revision'> & { access_token: string }
export interface SiteAccount {
  id: number
  name: string
  platform: string
  status: string
  source: 'imported' | 'linked'
  key_id: string
  models: Record<string, string>
  concurrency: number
  rate_multiplier: number
}
export interface ModelPreview {
  account_id: number
  version: string
  added: string[]
  removed: string[]
  mapping: Record<string, string>
}
export interface ImportPayload {
  key_id: string
  revision: number
  name: string
  platform: string
  group_id: number
  concurrency: number
  rate_multiplier: number
  models: string[]
}
export interface ImportDraft {
  payload: ImportPayload
  idempotencyKey: string
  status: 'pending' | 'running' | 'success' | 'failed'
  accountId?: number
  error?: string
}
const endpoint = '/admin/accounts/upstream-sites'
const sitePath = (id: string) => `${endpoint}/${encodeURIComponent(id)}`
export const upstreamSitesAPI = {
  async list(): Promise<UpstreamSite[]> { return (await apiClient.get(endpoint)).data },
  async save(id: string, form: SiteForm): Promise<UpstreamSite> {
    return (await (id ? apiClient.put(sitePath(id), form, { timeout: 60_000 }) : apiClient.post(endpoint, form, { timeout: 60_000 }))).data
  },
  async delete(id: string): Promise<void> { await apiClient.delete(sitePath(id)) },
  async sync(id: string): Promise<SiteInventory> { return (await apiClient.get(`${sitePath(id)}/inventory`, { timeout: 180_000 })).data },
  async models(id: string, keyId: string): Promise<string[]> { return (await apiClient.get(`${sitePath(id)}/keys/${encodeURIComponent(keyId)}/models`, { timeout: 120_000 })).data },
  async accounts(id: string): Promise<SiteAccount[]> { return (await apiClient.get(`${sitePath(id)}/accounts`)).data },
  async link(id: string, revision: number, accountId: number, keyId: string, unlink = false): Promise<void> {
    await apiClient.post(`${sitePath(id)}/accounts/link`, { revision, account_id: accountId, key_id: keyId, unlink }, { timeout: 120_000 })
  },
  async modelPreview(id: string, revision: number, accountId: number, preview?: ModelPreview): Promise<ModelPreview> {
    return (await apiClient.post(`${sitePath(id)}/accounts/${accountId}/models`, { revision, apply: !!preview, preview }, { timeout: 120_000 })).data
  },
  async createKey(id: string, revision: number, name: string, groupId: string): Promise<void> {
    await apiClient.post(`${sitePath(id)}/keys`, { revision, name, group_id: groupId, confirmed: true }, { timeout: 30_000 })
  },
  async keyGroups(id: string, keyId: string): Promise<SiteKeyGroups> {
    return (await apiClient.get(`${sitePath(id)}/keys/${encodeURIComponent(keyId)}/groups`, { timeout: 60_000 })).data
  },
  async changeKeyGroup(id: string, keyId: string, revision: number, currentGroupId: string, groupId: string): Promise<SiteKey> {
    return (await apiClient.put(`${sitePath(id)}/keys/${encodeURIComponent(keyId)}/group`, { revision, current_group_id: currentGroupId, group_id: groupId }, { timeout: 120_000 })).data
  },
  async importKey(id: string, draft: ImportDraft): Promise<{ account_id: number }> {
    return (await apiClient.post(`${sitePath(id)}/import`, draft.payload, { timeout: 120_000, headers: { 'Idempotency-Key': draft.idempotencyKey } })).data
  }
}

export function siteError(cause: unknown): string {
  return cause && typeof cause === 'object' && 'message' in cause && typeof cause.message === 'string' ? cause.message : String(cause)
}

// Keep the payload and idempotency key frozen across retries, including ambiguous timeouts.
export async function runSiteImports(siteId: string, drafts: ImportDraft[], stopped: () => boolean) {
  for (const draft of drafts) {
    if (stopped()) break
    if (draft.status === 'success') continue
    draft.status = 'running'; draft.error = undefined
    try {
      draft.accountId = (await upstreamSitesAPI.importKey(siteId, draft)).account_id
      draft.status = 'success'
    } catch (cause) { draft.status = 'failed'; draft.error = siteError(cause) }
  }
}
