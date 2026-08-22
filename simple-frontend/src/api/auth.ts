import { apiRequest } from './client'

export interface RegisterInput { email: string; password: string; nickname: string }
export interface LoginInput { email: string; password: string }

export const authApi = {
  register: (input: RegisterInput) => apiRequest('/api/v1/public/auth/register', { method: 'POST', body: input }),
  login: (input: LoginInput) => apiRequest<void>('/api/v1/public/auth/login', { method: 'POST', body: input }),
  logout: () => apiRequest('/api/v1/protected/auth/logout', { method: 'POST', scope: 'user', retryAuth: false }),
}
