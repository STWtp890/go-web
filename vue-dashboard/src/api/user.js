import api from './index'

export const userAPI = {
  getProfile: (userId) => api.get(`/user/profile/${userId}`),
  logout: (userId) => api.post(`/user/logout/${userId}`)
}
