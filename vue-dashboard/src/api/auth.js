import api from './index'

export const authAPI = {
  login: (data) => api.post('/auth/login', data),
  register: (data) => api.post('/auth/register', { registerInfo: data }),
  refreshToken: (refreshToken) => api.post('/auth/refresh', { refreshToken })
}
