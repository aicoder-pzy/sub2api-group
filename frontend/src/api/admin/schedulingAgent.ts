import { apiClient } from '../client'

export interface SchedulingPolicy {
	 pools?: SchedulingPool[]
	 observe_models?: string[]
  accounts: number[] | null
  models: Record<string, number[]> | null
  agent_mode: '' | 'off' | 'observe' | 'auto'
  daily_calls: number
  min_wait_ms: number
}

export interface SchedulingPool {
  id: string
  name: string
  accounts: number[]
  models?: Record<string, number[]>
}

export interface SchedulingEvidence {
  pool_id?: string
  pool_name?: string
  eligible?: boolean
  model: string
  account_id: number
  successes: number
  failures: number
  first_event_ms: number
  full_slots: number
  waited_ms: number
  cache_ratio?: number
  quality_tier?: string
  speed?: { output_tps: number; p95_ms: number }
  current?: boolean
  trial?: boolean
  recent_windows?: { age: number; effort: string; context_bucket: number; samples: number; p50_ms: number; p95_ms: number }[]
}

export interface SchedulingRun {
  at: string
  state: string
  calls: number
  input_tokens: number
  output_tokens: number
  expires: string
  decision?: { action: string; model: string; account_id: number; wait_ms: number; reason: string }
  inspections?: { model: string; effort?: string; hours: number; reason: string }[]
  evidence?: SchedulingEvidence[]
  feedback?: SchedulingEvidence[]
  applied_requests?: number
  completed_attempts?: number
  failed_attempts?: number
  routes?: Record<string, { account_id?: number; reason?: string; selected_at: string; trial_id?: number; trial_reason?: string; trial_until: string }>
}

export interface SchedulingStatus {
  policy: SchedulingPolicy
  run: SchedulingRun
  history?: SchedulingRun[]
  accounts: { id: number; name: string; site: string; upstream_group: string; rate?: number; available?: boolean; models?: string[] }[]
}

export interface SchedulingConnection {
  url: string
  model: string
  effort: string
  api_key?: string
  key_configured: boolean
  daily_calls: number
  interval_minutes: number
  clear_key?: boolean
}

const connectionPath = '/admin/settings/scheduling-agent'
const groupPath = (id: number) => `/admin/groups/${id}/scheduling-agent`
export const schedulingAgentAPI = {
  async status(id: number) { return (await apiClient.get<SchedulingStatus>(groupPath(id))).data },
  async save(id: number, policy: SchedulingPolicy) { return (await apiClient.put<SchedulingStatus>(groupPath(id), policy)).data },
  async run(id: number) { return (await apiClient.post<SchedulingStatus>(groupPath(id), undefined, { timeout: 305000 })).data },
  async revoke(id: number) { return (await apiClient.delete<SchedulingStatus>(groupPath(id))).data },
  async connection() { return (await apiClient.get<SchedulingConnection>(connectionPath)).data },
  async saveConnection(config: SchedulingConnection) { return (await apiClient.put<SchedulingConnection>(connectionPath, config)).data },
  async testConnection() { return (await apiClient.post<SchedulingRun>(`${connectionPath}/test`, undefined, { timeout: 95000 })).data },
}
