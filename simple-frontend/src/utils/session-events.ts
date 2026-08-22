import type { SessionScope } from '@/types/api'

type SessionEvent = { scope: SessionScope; type: 'signed-in' | 'signed-out' }
type SessionListener = (event: SessionEvent) => void

const listeners = new Set<SessionListener>()
const channel = typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('paperplane-session')

function emit(event: SessionEvent, broadcast: boolean): void {
  listeners.forEach((listener) => listener(event))
  if (broadcast) channel?.postMessage(event)
}

channel?.addEventListener('message', (event: MessageEvent<SessionEvent>) => {
  if (event.data?.type === 'signed-in' || event.data?.type === 'signed-out') emit(event.data, false)
})

export function notifySessionChange(scope: SessionScope, type: SessionEvent['type']): void {
  emit({ scope, type }, true)
}

export function subscribeSessionChanges(listener: SessionListener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}
