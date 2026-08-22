import { onBeforeUnmount, readonly, ref } from 'vue'

export type ChatConnectionState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected'

interface UseChatSocketOptions {
  getURL: () => string
  ensureSession: () => Promise<boolean>
  onMessage: (payload: string) => void
  onConnected?: () => void
  onUnauthenticated?: () => void
}

/**
 * Owns the browser WebSocket lifecycle for a chat page.
 *
 * Side effects: opens a WebSocket and schedules reconnect timers. Both are
 * cleaned up synchronously through the component lifecycle that calls it.
 */
export function useChatSocket(options: UseChatSocketOptions) {
  const connectionState = ref<ChatConnectionState>('connecting')
  let socket: WebSocket | null = null
  let reconnectTimer: number | undefined
  let reconnectAttempt = 0
  let disposed = false

  function cancelReconnect() {
    if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
    reconnectTimer = undefined
  }

  function scheduleReconnect() {
    if (disposed || reconnectTimer !== undefined) return
    const delay = Math.min(1_000 * 2 ** reconnectAttempt, 30_000)
    reconnectAttempt += 1
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = undefined
      void reconnect()
    }, delay)
  }

  async function reconnect() {
    if (disposed) return
    connectionState.value = 'reconnecting'
    const authenticated = await options.ensureSession()
    if (!authenticated) {
      connectionState.value = 'disconnected'
      options.onUnauthenticated?.()
      return
    }
    connect()
  }

  function connect() {
    if (disposed || socket?.readyState === WebSocket.OPEN || socket?.readyState === WebSocket.CONNECTING) return
    connectionState.value = reconnectAttempt ? 'reconnecting' : 'connecting'
    const nextSocket = new WebSocket(options.getURL())
    socket = nextSocket

    nextSocket.onopen = () => {
      if (socket !== nextSocket) return
      reconnectAttempt = 0
      connectionState.value = 'connected'
      options.onConnected?.()
    }
    nextSocket.onmessage = (event) => options.onMessage(String(event.data))
    nextSocket.onerror = () => nextSocket.close()
    nextSocket.onclose = () => {
      if (socket === nextSocket) socket = null
      if (disposed) return
      connectionState.value = 'reconnecting'
      scheduleReconnect()
    }
  }

  function retryNow() {
    if (disposed) return
    cancelReconnect()
    reconnectAttempt = 0
    if (socket?.readyState === WebSocket.CONNECTING) socket.close()
    void reconnect()
  }

  function disconnect() {
    disposed = true
    cancelReconnect()
    socket?.close()
    socket = null
    connectionState.value = 'disconnected'
  }

  onBeforeUnmount(disconnect)

  return {
    connectionState: readonly(connectionState),
    connect,
    disconnect,
    retryNow,
  }
}
