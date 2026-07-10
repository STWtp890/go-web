import api from './index'

export const markdownAPI = {
  list: (params) => api.get('/markdown/list', { params }),
  mine: (userId, params) => api.get(`/markdown/list/${userId}`, { params }),
  preview: (id) => api.get(`/markdown/preview/${id}`),
  upload: (data) => api.post('/markdown/upload', data),
  update: (id, data) => api.put(`/markdown/update/${id}`, data),
  delete: (id) => api.delete(`/markdown/delete/${id}`),

  // 审核（管理员）
  review: {
    list: (params) => api.get('/admin/markdown/reviews', { params }),
    unreviewed: (params) => api.get('/admin/markdown/unreviewed', { params }),
    getInfo: (id) => api.get(`/admin/markdown/review/${id}`),
    submit: (id, data) => api.put(`/admin/markdown/review/${id}`, data)
  }
}
