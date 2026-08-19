import type { SessionScope, TokenPair } from '@/types/api'

const keys: Record<SessionScope, string> = {
  user: 'paperplane:user-session',
  manager: 'paperplane:manager-session',
}

const channel = typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('paperplane-session')

export function readTokenPair(scope: SessionScope): TokenPair | null {
  const raw = localStorage.getItem(keys[scope])
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Partial<TokenPair>
    return parsed.accessToken && parsed.refreshToken ? (parsed as TokenPair) : null
  } catch {
    localStorage.removeItem(keys[scope])
    return null
  }
}

export function writeTokenPair(scope: SessionScope, pair: TokenPair): void {
  localStorage.setItem(keys[scope], JSON.stringify(pair))
  channel?.postMessage({ type: 'session-changed', scope })
}

export function clearTokenPair(scope: SessionScope): void {
  localStorage.removeItem(keys[scope])
  channel?.postMessage({ type: 'session-changed', scope })
}

export function subscribeSessionChanges(callback: (scope: SessionScope) => void): () => void {
  const onStorage = (event: StorageEvent) => {
    const scope = (Object.entries(keys).find(([, key]) => key === event.key)?.[0] ?? null) as SessionScope | null
    if (scope) callback(scope)
  }
  const onChannel = (event: MessageEvent<{ type: string; scope: SessionScope }>) => {
    if (event.data.type === 'session-changed') callback(event.data.scope)
  }
  window.addEventListener('storage', onStorage)
  channel?.addEventListener('message', onChannel)
  return () => {
    window.removeEventListener('storage', onStorage)
    channel?.removeEventListener('message', onChannel)
  }
}

export function decodeJwtPayload(token: string): Record<string, unknown> | null {
  try {
    const payload = token.split('.')[1]
    if (!payload) return null
    const normalized = payload.replace(/-/g, '+').replace(/_/g, '/')
    return JSON.parse(decodeURIComponent(escape(atob(normalized)))) as Record<string, unknown>
  } catch {
    return null
  }
}
