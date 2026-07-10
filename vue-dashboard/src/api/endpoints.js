// Re-export all API modules from domain-specific files.
// Prefer importing directly from '@/{api}/auth' etc. in new code.
export { authAPI } from './auth'
export { announcementAPI } from './announcement'
export { markdownAPI } from './markdown'
export { roleAPI, permissionAPI } from './rbac'
export { userAPI } from './user'
export { healthAPI } from './health'
