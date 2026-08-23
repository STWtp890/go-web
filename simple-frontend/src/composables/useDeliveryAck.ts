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

  function flushAcks(): Promise<void> {
    if (flushPromise) return flushPromise
    flushPromise = (async () => {
      while (pendingAcks.value.length) {
        const batch = pendingAcks.value.slice(0, batchSize)
        try {
          await options.sendAck(batch)
        } catch {
          return
        }
        const completed = new Set(batch)
        pendingAcks.value = pendingAcks.value.filter((id) => !completed.has(id))
        persistAcks()
      }
    })().finally(() => {
      flushPromise = null
    })
    return flushPromise
  }

  return {
    pendingAcks: readonly(pendingAcks),
    enqueueAck,
    flushAcks,
    restoreAcks,
  }
}
