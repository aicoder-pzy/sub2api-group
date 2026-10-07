import { apiClient } from '../client'

export type ProxySource = 'account' | 'static' | 'mihomo'
export interface BPSTicketSettings {
  harvest_enabled: boolean
  target_length: number
  target_gateway: string
  ttl_seconds: number
  refresh_before_seconds: number
  harvest_interval_seconds: number
  attempt_timeout_seconds: number
  max_attempts: number
  fail_closed: boolean
  harvest_proxy_source: ProxySource
  proxy_ids: number[]
  auto_probe_enabled: boolean
  probe_interval_seconds: number
  degraded_threshold: number
  healthy_threshold: number
}
export interface BPSTicketAccountConfig {
  bps: boolean
  models: string[]
  proxy_source: ProxySource
  tickets: boolean
  auto_probe: boolean
  auto_switch: boolean
}
export interface StateProbeResult {
  verdict: 'healthy' | 'degraded' | 'inconclusive'
  model: string
  reason: string
  detail?: string
  ticket_length: number
  continue_ticket_length: number
  mint_status: number
  continue_status: number
  latency_ms: number
  finished_at: string
}
export interface TicketStatus {
  model: string
  length: number
  ready: boolean
  expires_at: string
  checked_at: string
  attempts: number
  reason: string
  proxy_id?: number
  node?: string
}
export interface BPSTicketAccount {
  id: number
  name: string
  status: string
  schedulable: boolean
  bps_eligible: boolean
  config: BPSTicketAccountConfig
  state: { models: Record<string, { probe?: StateProbeResult; auto_bps: boolean; degraded_count: number; healthy_count: number }> }
  tickets: TicketStatus[]
}
export interface BPSTicketDashboard {
  settings: BPSTicketSettings
  accounts: BPSTicketAccount[]
  limitation: string
}
const root = '/admin/bps-tickets'
export const bpsTicketsApi = {
  async get() { return (await apiClient.get<BPSTicketDashboard>(root)).data },
  async saveSettings(value: BPSTicketSettings) { await apiClient.put(`${root}/settings`, value) },
  async saveAccount(id: number, value: BPSTicketAccountConfig) { await apiClient.put(`${root}/accounts/${id}`, value) },
  async probe(id: number, model: string) { return (await apiClient.post<StateProbeResult>(`${root}/accounts/${id}/probe`, { model }, { timeout: 200000 })).data },
  async harvest(id: number, model: string) { return (await apiClient.post<TicketStatus>(`${root}/accounts/${id}/harvest`, { model }, { timeout: 600000 })).data }
}
