import { apiRequest } from './client'

export const chatApi = {
  myGroups: async () => (await apiRequest<{ groups: string[] }>('/api/v1/protected/chat/groups/mine', { scope: 'user' })).data.groups,
  members: async (groupId: string) => (await apiRequest<{ groupId: string; members: string[] }>(`/api/v1/protected/chat/groups/${encodeURIComponent(groupId)}/members`, { scope: 'user' })).data.members,
  join: async (groupId: string) => (await apiRequest<{ groupId: string; memberId: string; supplement: unknown[] }>(`/api/v1/protected/chat/groups/${encodeURIComponent(groupId)}/join`, { method: 'POST', scope: 'user' })).data,
  leave: async (groupId: string) => (await apiRequest<{ groupId: string; memberId: string }>(`/api/v1/protected/chat/groups/${encodeURIComponent(groupId)}/leave`, { method: 'POST', scope: 'user' })).data,
  acknowledge: (deliveryId: string) => apiRequest<void>(`/api/v1/protected/chat/deliveries/${encodeURIComponent(deliveryId)}/ack`, { method: 'POST', scope: 'user' }),
}
