import { apiClient } from '../client'

export interface PelicanAssessmentRecord {
  hash: string
  status: 'running' | 'succeeded' | 'failed'
  assessment?: { quality: 'normal' | 'degraded' | 'unknown'; reason: string; source: string; checked_at: string }
  error?: string
  retry_after_seconds?: number
}

// Calls stay on our backend; its separate HTML-only client contacts Manxue.
export const pelicanAssessmentsAPI = {
  async lookup(html: string, signal?: AbortSignal): Promise<PelicanAssessmentRecord | null> {
    const { data } = await apiClient.post<PelicanAssessmentRecord | null>('/admin/pelican-assessments/lookup', { html }, { signal })
    return data
  },
  async start(html: string, signal?: AbortSignal): Promise<PelicanAssessmentRecord> {
    const { data } = await apiClient.post<PelicanAssessmentRecord>('/admin/pelican-assessments', { html }, { signal })
    return data
  },
  async poll(hash: string, signal?: AbortSignal): Promise<PelicanAssessmentRecord> {
    const { data } = await apiClient.get<PelicanAssessmentRecord>(`/admin/pelican-assessments/${hash}`, { signal })
    return data
  },
}
