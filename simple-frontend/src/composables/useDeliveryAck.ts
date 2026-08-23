import { readonly, ref } from 'vue'

const DEFAULT_BATCH_SIZE = 100

interface UseDeliveryAckOptions {
  storageKey: string
  sendAck: (deliveryIds: string[]) => Promise<unknown>
  batchSize?: number
}

// useDeliveryAck 管理“客户端应用已成功处理 delivery”的短期确认队列。
// 它不表达消息已读，也不维护任何阅读状态。
export function useDeliveryAck(options: UseDeliveryAckOptions) {
  const batchSize = options.batchSize ?? DEFAULT_BATCH_SIZE
  const pendingAcks = ref<string[]>([])
  let flushPromise: Promise<void> | null = null
  let queueGeneration = 0

  function persistAcks() {
    try {
      sessionStorage.setItem(options.storageKey, JSON.stringify(pendingAcks.value))
    } catch {
      // 内存队列仍可继续重试；storage 不可用不改变 ACK 语义。
    }
  }

  function restoreAcks() {
    try {
      const stored = JSON.parse(sessionStorage.getItem(options.storageKey) ?? '[]')
      if (!Array.isArray(stored)) return
      pendingAcks.value = [...new Set(stored.filter((id): id is string => typeof id === 'string' && id.length > 0))]
    } catch {
      pendingAcks.value = []
    }
  }

  function enqueueAck(deliveryId: string) {
    if (!deliveryId || pendingAcks.value.includes(deliveryId)) return
    pendingAcks.value = [...pendingAcks.value, deliveryId]
    persistAcks()
  }

  function clearAcks() {
    queueGeneration += 1
    pendingAcks.value = []
    try {
      sessionStorage.removeItem(options.storageKey)
    } catch {
      // The in-memory queue has still been cleared for the old user session.
    }
  }

  function flushAcks(): Promise<void> {
    if (flushPromise) return flushPromise
    const flushGeneration = queueGeneration
    flushPromise = (async () => {
      while (pendingAcks.value.length) {
        const batch = pendingAcks.value.slice(0, batchSize)
        try {
          await options.sendAck(batch)
        } catch {
          return
        }
        if (flushGeneration !== queueGeneration) return
        const completed = new Set(batch)
        pendingAcks.value = pendingAcks.value.filter((id) => !completed.has(id))
        persistAcks()
      }
    })().finally(() => {
      flushPromise = null
      if (flushGeneration !== queueGeneration && pendingAcks.value.length) void flushAcks()
    })
    return flushPromise
  }

  return {
    pendingAcks: readonly(pendingAcks),
    enqueueAck,
    clearAcks,
    flushAcks,
    restoreAcks,
  }
}
