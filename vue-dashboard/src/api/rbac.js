import api from './index'

export const roleAPI = {
  list: (params) => api.get('/admin/rbac/roles/list', { params }),
  create: (data) => api.post('/admin/rbac/roles/create', data),
  update: (id, data) => api.put(`/admin/rbac/roles/update/${id}`, { role: data }),
  delete: (id) => api.delete(`/admin/rbac/roles/delete/${id}`),
  assign: (userId, roles) => api.post(`/admin/rbac/roles/assign/${userId}`, { roles })
}

export const permissionAPI = {
  list: (params) => api.get('/admin/rbac/permissions/list', { params }),
  create: (data) => api.post('/admin/rbac/permissions/create', { permission: data }),
  update: (id, data) => api.put(`/admin/rbac/permissions/update/${id}`, { permission: data }),
  delete: (id) => api.delete(`/admin/rbac/permissions/delete/${id}`),
  assign: (id, permissions) => api.post(`/admin/rbac/permissions/assign/${id}`, { permissions })
}
