import { apiClient } from '../client'

export interface RecipientPreview { recipients: string[]; invalid: string[]; duplicates: number }
export type BroadcastContentFormat = 'text' | 'html'
export interface BroadcastInput { id: string; recipients: string; subject: string; content: string; content_format: BroadcastContentFormat; unsubscribe_url: string; interval_seconds: number }
export interface Broadcast {
  id: string; subject: string; content?: string; content_format: BroadcastContentFormat; interval_seconds: number; status: string; created_at: string
  total: number; pending: number; sending: number; sent: number; failed: number; unknown: number; cancelled: number
}
export interface BroadcastDetail { broadcast: Broadcast; recipients: { email: string; status: string; error: string }[]; total: number }

export async function preview(recipients: string): Promise<RecipientPreview> {
  return (await apiClient.post<RecipientPreview>('/admin/email-broadcasts/preview', { recipients })).data
}
export async function create(input: BroadcastInput): Promise<BroadcastDetail> {
  return (await apiClient.post<BroadcastDetail>('/admin/email-broadcasts', input)).data
}
export async function list(page: number): Promise<{ items: Broadcast[] }> {
  return (await apiClient.get<{ items: Broadcast[] }>('/admin/email-broadcasts', { params: { page } })).data
}
export async function detail(id: string, status: string, page: number): Promise<BroadcastDetail> {
  return (await apiClient.get<BroadcastDetail>(`/admin/email-broadcasts/${id}`, { params: { status, page } })).data
}
export async function cancel(id: string): Promise<void> { await apiClient.post(`/admin/email-broadcasts/${id}/cancel`) }
export async function test(recipient: string, subject: string, content: string, contentFormat: BroadcastContentFormat, unsubscribeUrl: string): Promise<void> {
  await apiClient.post('/admin/email-broadcasts/test', { recipient, subject, content, content_format: contentFormat, unsubscribe_url: unsubscribeUrl })
}
