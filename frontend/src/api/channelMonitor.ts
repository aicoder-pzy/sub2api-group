/**
 * User-facing Channel Monitor API endpoints
 * Read-only views for end users to inspect channel availability/status.
 */

import { apiClient } from './client'
import type { MonitorQuotaSnapshot, Provider, MonitorStatus } from './admin/channelMonitor'

export type { Provider, MonitorStatus } from './admin/channelMonitor'

export interface UserMonitorExtraModel {
  intelligence?: MonitorIntelligence
  model: string
  status: MonitorStatus | ''
  latency_ms: number | null
  ping_latency_ms?: number | null
  availability_7d?: number | null
  timeline?: MonitorTimelinePoint[]
}

export interface MonitorTimelinePoint {
  status: MonitorStatus
  latency_ms: number | null
  ping_latency_ms: number | null
  checked_at: string
}

export interface MonitorIntelligencePoint {
  status: 'normal' | 'degraded' | 'incorrect' | 'failed' | 'unknown'
  checked_at: string
  latency_ms: number
  effort?: string
    prompt?: string
    prompt_truncated?: boolean
  answer?: string
  expected_answer?: string
  answer_truncated?: boolean
    confirmation_pending?: boolean
}

export interface MonitorIntelligenceTest {
  id: number
  kind: string
  name?: string
  state: string
  history: MonitorIntelligencePoint[]
}

export interface MonitorIntelligence {
  state: string
  history: MonitorIntelligencePoint[]
  tests?: MonitorIntelligenceTest[]
}

export interface MonitorIntelligenceView {
  id: number
  name: string
  primary_model: string
  intelligence: MonitorIntelligence
}

export interface UserMonitorView {
  intelligence?: MonitorIntelligence
  id: number
  name: string
  provider: Provider
  group_name: string
  primary_model: string
  primary_status: MonitorStatus | ''
  primary_latency_ms: number | null
  primary_ping_latency_ms: number | null
  availability_7d: number
  has_availability_7d?: boolean
  extra_models: UserMonitorExtraModel[]
  timeline: MonitorTimelinePoint[]
  /**
   * 主模型最近配额快照。仅当系统开启 channel_monitor_show_quota 时
   * 服务端才会下发（关闭时服务端已剥离，前端 flag 仅作纵深防御）。
   */
  latest_quota?: MonitorQuotaSnapshot | null
}

export interface UserMonitorListResponse {
  items: UserMonitorView[]
}

export interface UserMonitorModelDetail {
  model: string
  latest_status: MonitorStatus
  latest_latency_ms: number | null
  availability_7d: number
  availability_15d: number
  availability_30d: number
  avg_latency_7d_ms: number | null
}

export interface UserMonitorDetail {
  id: number
  name: string
  provider: Provider
  group_name: string
  models: UserMonitorModelDetail[]
}

/**
 * List all monitor views available to the current user.
 */
export async function list(options?: { signal?: AbortSignal }): Promise<UserMonitorListResponse> {
  const { data } = await apiClient.get<UserMonitorListResponse>('/channel-monitors', {
    signal: options?.signal,
  })
  return data
}

/**
 * Get detailed status (multi-window availability + latency) for a single monitor.
 */
export async function status(id: number): Promise<UserMonitorDetail> {
  const { data } = await apiClient.get<UserMonitorDetail>(`/channel-monitors/${id}/status`)
  return data
}

export const channelMonitorUserAPI = {
  list,
  status,
}

export default channelMonitorUserAPI

export async function listIntelligence(signal?: AbortSignal): Promise<MonitorIntelligenceView[]> {
  const { data } = await apiClient.get<{ items: MonitorIntelligenceView[] }>('/channel-monitor-v2/intelligence', { signal })
  return data.items ?? []
}
