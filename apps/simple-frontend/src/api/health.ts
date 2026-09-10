import { apiRequest } from './client'

export const healthApi = {
  live: () => apiRequest<{ status: string; service: string }>('/healthz'),
  ready: () => apiRequest<{ status: string; service?: string }>('/readyz'),
}
