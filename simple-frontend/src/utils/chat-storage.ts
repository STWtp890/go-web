export const CHAT_HISTORY_STORAGE_KEY = 'paperplane:chat-history-v3'
export const CHAT_PENDING_ACK_STORAGE_KEY = 'paperplane:chat-pending-acks-v2'

const chatSessionStorageKeys = [CHAT_HISTORY_STORAGE_KEY, CHAT_PENDING_ACK_STORAGE_KEY]

// Chat state belongs to the authenticated user session. Remove it whenever the
// user authentication boundary changes so another account cannot restore it.
export function clearChatSessionStorage(): void {
  try {
    chatSessionStorageKeys.forEach((key) => sessionStorage.removeItem(key))
  } catch {
    // Storage may be unavailable in restricted browser contexts.
  }
}
