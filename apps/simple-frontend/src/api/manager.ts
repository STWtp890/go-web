import { apiRequest } from './client'
import type { ApiMeta, PageResult } from '@/types/api'
import type { RegistrationRequest } from '@/types/domain'

interface RawRegistrationRequest {
  id: number
  username: string
  email: string
  reason: string
  status: RegistrationRequest['status']
  reviewer_id: number
  review_comment: string
  reviewed_at: string | null
  created_at: number
  updated_at: number
}

function normalize(item: RawRegistrationRequest): RegistrationRequest {
  return {
    id: item.id,
    username: item.username,
    email: item.email,
    reason: item.reason,
    status: item.status,
    reviewerId: item.reviewer_id,
    reviewComment: item.review_comment,
    reviewedAt: item.reviewed_at,
    createdAt: item.created_at,
    updatedAt: item.updated_at,
  }
}

export const managerApi = {
  register: (input: { username: string; password: string; email?: string; reason?: string }) => apiRequest<{ requestId: number; username: string; status: 'pending' }>('/api/v1/public/manager/register', { method: 'POST', body: input }),
  login: (input: { username: string; password: string }) => apiRequest<void>('/api/v1/public/manager/login', { method: 'POST', body: input }),
  logout: () => apiRequest('/api/v1/protected/manager/logout', { method: 'POST', scope: 'manager', retryAuth: false }),
  requests: async (status: RegistrationRequest['status'], page = 1, pageSize = 10): Promise<PageResult<RegistrationRequest>> => {
    const result = await apiRequest<{ requests: RawRegistrationRequest[] }>(`/api/v1/protected/manager/requests?status=${status}&page=${page}&pageSize=${pageSize}`, { scope: 'manager' })
    return { items: result.data.requests.map(normalize), meta: result.meta as ApiMeta }
  },
  approve: (id: number, comment: string) => apiRequest(`/api/v1/protected/manager/requests/${id}/approve`, { method: 'POST', scope: 'manager', body: { comment } }),
  reject: (id: number, comment: string) => apiRequest(`/api/v1/protected/manager/requests/${id}/reject`, { method: 'POST', scope: 'manager', body: { comment } }),
}
