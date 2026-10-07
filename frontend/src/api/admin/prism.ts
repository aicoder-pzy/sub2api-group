import { apiClient } from '../client'

export interface PrismSettings {
  enabled: boolean
  base_url: string
  api_key_configured: boolean
}
export interface PrismDashboard {
  settings: PrismSettings
  models: string[]
  efforts: string[]
  accounts: { id: number; name: string; status: string }[]
}
export interface PrismTestInput { model: string; effort: string; prompt: string }
export interface PrismTestResult {
  success: boolean
  model: string
  effort: string
  request_id: string
  text: string
  duration_ms: number
  usage_available: boolean
}

export const prismApi = {
  async get() { return (await apiClient.get<PrismDashboard>('/admin/prism')).data },
  async save(input: { enabled: boolean; base_url: string; api_key: string }) {
    return (await apiClient.put<PrismSettings>('/admin/prism/settings', input)).data
  },
  async health() { return (await apiClient.post<{ process_ready: boolean }>('/admin/prism/health')).data },
  async test(id: number, input: PrismTestInput) {
    return (await apiClient.post<PrismTestResult>(`/admin/prism/accounts/${id}/test`, input, { timeout: 310000 })).data
  }
}
