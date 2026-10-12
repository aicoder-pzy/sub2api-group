import { apiClient } from '../client'

export interface ChannelStatisticsRow {
  day: string
  account_id: number
  account_name: string
  account_deleted: boolean
  requests: number
  successes: number
  failures: number
  unclassified_failures: number
  stream_requests: number
  nonstream_requests: number
  latency_samples: number
  ttft_average_ms: number | null
  ttft_p50_ms: number | null
  ttft_p95_ms: number | null
  slow_30: number
  slow_60: number
  duration_p50_ms: number | null
  duration_p95_ms: number | null
  output_tps_p50: number | null
  context_tokens_p50: number | null
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  standard_cost: number
  account_cost: number
  user_cost: number
  normal: number
  degraded: number
  unknown: number
  failed_tests: number
}

export interface ChannelStatistics {
  start_date: string
  end_date: string
  timezone: string
  generated_at: string
  summary: ChannelStatisticsRow
  accounts: ChannelStatisticsRow[]
  daily: ChannelStatisticsRow[]
  models: string[]
}

export interface ChannelStatisticsQuery {
  start_date: string
  end_date: string
  group_id?: number
  model?: string
  effort?: string
}

export async function getChannelStatistics(params: ChannelStatisticsQuery, signal?: AbortSignal) {
  const { data } = await apiClient.get<ChannelStatistics>('/admin/usage/channel-statistics', { params, signal })
  return data
}
