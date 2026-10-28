import { apiClient } from '../client'

export interface Upstream { id: number; name: string; members: number }
export interface Member { id: number; name: string; platform: string; status: string; schedulable: boolean; concurrency: number; upstream_id: number; eligible: boolean }
export interface Catalog { revision: number; upstreams: Upstream[]; upstream_total: number; accounts: Member[]; total: number }
export interface Mutation { action: 'create_upstream' | 'update_upstream' | 'delete_upstream' | 'assign' | 'detach'; revision: number; upstream_id?: number; account_id?: number; expected_upstream_id?: number; name?: string; confirm?: boolean }
export interface Metrics { requests: number; samples: number; unknown_samples: number; duration_samples: number; p50_ms: number | null; p90_ms: number | null; p95_ms: number | null; duration_p90_ms: number | null; account_cost: number; user_cost: number; latest_sample_at: string | null }
export interface UsageRow extends Metrics { dimension_id: number; name: string }
export interface Lineage { public_model: string; recorded_model: string; upstream_model: string | null; model_mapping_chain: string | null; samples: number }
export interface Usage { summary: Metrics; total: number; rows: UsageRow[]; lineage: Lineage[]; lineage_total: number }
export interface Quality { summary: { normal: number; degraded: number; unknown: number; failed: number }; rows: Array<{ id: number; account_id: number; model: string; effort: string; verdict: string; reason: string; sampled_at: string }>; has_more: boolean }
export interface ErrorLogs { records: number; bucket_total: number; rows: Array<{ error_type: string; error_phase: string; status_code: number | null; records: number }> }
export interface Capacity { accounts: number; schedulable: number; configured_concurrency: number }
export interface Section<T> { status: 'ready' | 'query_failed' | 'unsupported_scope'; data: T | null; error?: string; queried_at: string }
export interface Snapshot { scope: { channel_id: number; upstream_id: number; account_id: number; group_id: number; model: string; model_scope: string; dimension: string }; usage: Section<Usage>; quality: Section<Quality>; errors: Section<ErrorLogs>; capacity: Section<Capacity>; from: string; until: string }
export interface CatalogQuery { page: number; page_size: number; search: string; upstream_page: number; upstream_search: string; upstream_id: number; membership: string }
export interface SnapshotQuery { channel_id: number; upstream_id: number; account_id: number; group_id: number; from: string; until: string; model: string; model_scope: string; dimension: string; page: number; page_size: number; quality_before: number }
export async function catalog(id: number, params: CatalogQuery, signal?: AbortSignal): Promise<Catalog> { return (await apiClient.get<Catalog>(`/admin/channels/${id}/operations`, { params, signal })).data }
export async function mutate(id: number, body: Mutation): Promise<{ revision: number }> { return (await apiClient.post<{ revision: number }>(`/admin/channels/${id}/operations`, body)).data }
export async function snapshot(params: SnapshotQuery, signal?: AbortSignal): Promise<Snapshot> { return (await apiClient.get<Snapshot>('/admin/channels/operations', { params, signal })).data }
