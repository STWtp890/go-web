import api from './index'

export const healthAPI = {
  healthz: () => api.get('/status/healthz'),
  readyz: () => api.get('/status/readyz')
}
