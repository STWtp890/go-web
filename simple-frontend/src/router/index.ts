import { createRouter, createWebHistory } from 'vue-router'
import { useManagerSessionStore, useUserSessionStore } from '@/stores/session'

const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0, behavior: 'smooth' }),
  routes: [
    { path: '/', name: 'landing', component: () => import('@/views/LandingView.vue'), meta: { guestOnly: true } },
    { path: '/login', name: 'login', component: () => import('@/views/auth/UserAuthView.vue'), meta: { guestOnly: true } },
    { path: '/register', name: 'register', component: () => import('@/views/auth/UserAuthView.vue'), meta: { guestOnly: true } },
    {
      path: '/app',
      component: () => import('@/layouts/AppLayout.vue'),
      meta: { requiresUser: true },
      children: [
        { path: '', name: 'overview', component: () => import('@/views/app/OverviewView.vue') },
        { path: 'mine', name: 'mine', component: () => import('@/views/app/LibraryView.vue'), props: { mode: 'mine' } },
        { path: 'explore', name: 'explore', component: () => import('@/views/app/LibraryView.vue'), props: { mode: 'public' } },
        { path: 'search', name: 'search', component: () => import('@/views/app/LibraryView.vue'), props: { mode: 'search' } },
        { path: 'new', name: 'editor', component: () => import('@/views/app/MarkdownEditorView.vue') },
        { path: 'markdown/:markdownId', name: 'markdown-detail', component: () => import('@/views/app/MarkdownDetailView.vue') },
        { path: 'chat', name: 'chat', component: () => import('@/views/app/ChatView.vue') },
      ],
    },
    { path: '/manager/login', name: 'manager-login', component: () => import('@/views/manager/ManagerAccessView.vue'), meta: { managerGuestOnly: true } },
    { path: '/manager/apply', name: 'manager-apply', component: () => import('@/views/manager/ManagerAccessView.vue') },
    {
      path: '/manager',
      component: () => import('@/layouts/ManagerLayout.vue'),
      meta: { requiresManager: true },
      children: [{ path: 'requests', name: 'manager-requests', component: () => import('@/views/manager/ManagerRequestsView.vue') }],
    },
    { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('@/views/NotFoundView.vue') },
  ],
})

router.beforeEach(async (to) => {
  const userSession = useUserSessionStore()
  const managerSession = useManagerSessionStore()
  const needsUserCheck = Boolean(to.meta.requiresUser || to.meta.guestOnly)
  const needsManagerCheck = Boolean(to.meta.requiresManager || to.meta.managerGuestOnly)
  const hasUser = needsUserCheck && await userSession.restore()
  const hasManager = needsManagerCheck && await managerSession.restore()
  if (to.meta.requiresUser && !hasUser) return { name: 'login', query: { redirect: to.fullPath } }
  if (to.meta.requiresManager && !hasManager) return { name: 'manager-login', query: { redirect: to.fullPath } }
  if (to.meta.guestOnly && hasUser) return { name: 'overview' }
  if (to.meta.managerGuestOnly && hasManager) return { name: 'manager-requests' }
  return true
})

export default router
