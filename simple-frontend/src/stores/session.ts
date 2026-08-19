import { computed, onScopeDispose, ref } from 'vue'
import { defineStore } from 'pinia'
import { authApi } from '@/api/auth'
import { managerApi } from '@/api/manager'
import type { SessionScope, TokenPair } from '@/types/api'
import { clearTokenPair, decodeJwtPayload, readTokenPair, subscribeSessionChanges, writeTokenPair } from '@/utils/session-vault'

function createSessionStore(scope: SessionScope) {
  const tokenPair = ref<TokenPair | null>(readTokenPair(scope))
  const isAuthenticated = computed(() => Boolean(tokenPair.value?.accessToken))
  const claims = computed(() => tokenPair.value ? decodeJwtPayload(tokenPair.value.accessToken) : null)
  const subject = computed(() => String(claims.value?.sub ?? ''))
  const expiresAt = computed(() => Number(claims.value?.exp ?? 0))

  function setSession(pair: TokenPair) {
    writeTokenPair(scope, pair)
    tokenPair.value = pair
  }

  function clearSession() {
    clearTokenPair(scope)
    tokenPair.value = null
  }

  const unsubscribe = subscribeSessionChanges((changedScope) => {
    if (changedScope === scope) tokenPair.value = readTokenPair(scope)
  })
  onScopeDispose(unsubscribe)

  return { tokenPair, isAuthenticated, claims, subject, expiresAt, setSession, clearSession }
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
