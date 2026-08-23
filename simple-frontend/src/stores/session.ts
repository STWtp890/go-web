import { computed, onScopeDispose, ref } from 'vue'
import { defineStore } from 'pinia'
import { authApi } from '@/api/auth'
import { apiRequest } from '@/api/client'
import { managerApi } from '@/api/manager'
import type { SessionScope } from '@/types/api'
import { clearChatSessionStorage } from '@/utils/chat-storage'
import { notifySessionChange, subscribeSessionChanges } from '@/utils/session-events'

function createSessionStore(scope: SessionScope) {
  const status = ref<'unknown' | 'authenticated' | 'unauthenticated'>('unknown')
  const isAuthenticated = computed(() => status.value === 'authenticated')
  const subject = ref('')

  function setSession() {
    status.value = 'authenticated'
    notifySessionChange(scope, 'signed-in')
  }

  function clearSession() {
    status.value = 'unauthenticated'
    subject.value = ''
    notifySessionChange(scope, 'signed-out')
  }

  async function restore(force = false): Promise<boolean> {
    if (!force && isAuthenticated.value) return true
    const probePath = scope === 'user'
      ? '/api/v1/protected/markdown/mine?page=1&pageSize=1'
      : '/api/v1/protected/manager/requests?status=pending&page=1&pageSize=1'
    try {
      await apiRequest(probePath, { scope })
      status.value = 'authenticated'
      return true
    } catch {
      status.value = 'unauthenticated'
      return false
    }
  }

  const unsubscribe = subscribeSessionChanges((event) => {
    if (event.scope !== scope) return
    if (scope === 'user') clearChatSessionStorage()
    status.value = event.type === 'signed-in' ? 'authenticated' : 'unauthenticated'
    if (event.type === 'signed-out') subject.value = ''
  })
  onScopeDispose(unsubscribe)

  return { isAuthenticated, subject, setSession, clearSession, restore }
}

export const useUserSessionStore = defineStore('user-session', () => {
  const session = createSessionStore('user')
  async function logout() {
    try { await authApi.logout() } finally { session.clearSession() }
  }
  return { ...session, logout }
})

export const useManagerSessionStore = defineStore('manager-session', () => {
  const session = createSessionStore('manager')
  async function logout() {
    try { await managerApi.logout() } finally { session.clearSession() }
  }
  return { ...session, logout }
})
