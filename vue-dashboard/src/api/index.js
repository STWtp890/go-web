import axios from 'axios'
import { useAuthStore } from '@/stores/auth'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
  headers: { 'Content-Type': 'application/json' }
})

// Request interceptor
api.interceptors.request.use(
  (config) => {
    const auth = useAuthStore()
    if (auth.accessToken) {
      config.headers.Authorization = `Bearer ${auth.accessToken}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// Response interceptor with auto-refresh
let isRefreshing = false
let refreshQueue = []

api.interceptors.response.use(
  (response) => {
    const data = response.data
    // 兜底：即使 HTTP 200，若 body.code ≠ 200 也 reject（兼容旧版后端）
    if (data && typeof data.code === 'number' && data.code !== 200) {
      const err = new Error(data.message || 'Request failed')
      err.response = response
      err.code = data.code
      return Promise.reject(err)
    }
    return data
  },
  async (error) => {
    const originalRequest = error.config
    const auth = useAuthStore()

    if (error.response && error.response.status === 401 && !originalRequest._retry && auth.refreshToken) {
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          refreshQueue.push({ resolve, reject })
        }).then((token) => {
          originalRequest.headers.Authorization = `Bearer ${token}`
          return api(originalRequest)
        })
      }

      originalRequest._retry = true
      isRefreshing = true

      try {
        const res = await axios.post('/api/v1/auth/refresh', {
          refreshToken: auth.refreshToken
        }, { headers: { 'Content-Type': 'application/json' } })

        const data = res.data
        auth.setTokens(data.accessToken, data.refreshToken, data.expiresIn)

        refreshQueue.forEach(({ resolve }) => resolve(data.accessToken))
        refreshQueue = []

        originalRequest.headers.Authorization = `Bearer ${data.accessToken}`
        return api(originalRequest)
      } catch (refreshError) {
        refreshQueue.forEach(({ reject }) => reject(refreshError))
        refreshQueue = []
        auth.logout()
        window.location.href = '/#/login'
        return Promise.reject(refreshError)
      } finally {
        isRefreshing = false
      }
    }

    return Promise.reject(error)
  }
)

export default api
