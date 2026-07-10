import api from './index'

export const announcementAPI = {
  list: (params) => api.get('/announcement/list', { params }),
  preview: (id) => api.get(`/announcement/preview/${id}`),
  create: (data) => api.post('/admin/announcement/create', data),
  update: (id, data) => api.put(`/admin/announcement/update/${id}`, data),
  delete: (id) => api.delete(`/admin/announcement/delete/${id}`)
}
