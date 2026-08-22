<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CheckCheck, CircleAlert, LogOut, MessageCircle, Plus, Radio, RefreshCw, Send, UserPlus, Users, WifiOff } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { chatApi } from '@/api/chat'
import { API_BASE, getApiError } from '@/api/client'
import EmptyState from '@/components/EmptyState.vue'
import { useChatSocket, type ChatConnectionState } from '@/composables/useChatSocket'
import { useUserSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import type { SentMessage } from '@/types/domain'
import { formatRelativeDate } from '@/utils/format'

const HISTORY_KEY = 'paperplane:chat-history-v2'
const PENDING_ACK_KEY = 'paperplane:chat-pending-acks-v2'
const MAX_MESSAGES = 150

type ConversationKind = 'private' | 'group'

interface Conversation {
  id: string
  kind: ConversationKind
  title: string
  updatedAt: number
  preview: string
  unread: number
}

const router = useRouter()
const toast = useToastStore()
const session = useUserSessionStore()

// Conversation state
const messages = ref<SentMessage[]>([])
const groups = ref<string[]>([])
const members = ref<string[]>([])
const activeConversationId = ref('')
const unreadByConversation = ref<Record<string, number>>({})
const newPeerId = ref('')
const groupIdToJoin = ref('')
const content = ref('')
const sending = ref(false)
const joining = ref(false)
const groupsLoading = ref(false)
const membersLoading = ref(false)
const errorMessage = ref('')

// Delivery acknowledgement state. IDs contain no credential material.
const pendingAcknowledgements = ref<string[]>([])
let acknowledgementInFlight = false

function privateConversationId(userId: string): string {
  return `private:${userId}`
}

function groupConversationId(groupId: string): string {
  return `group:${groupId}`
}

function conversationIdForMessage(message: SentMessage): string {
  if (message.groupType === 'group') return groupConversationId(message.to)
  return privateConversationId(message.direction === 'received' ? (message.from ?? message.to) : message.to)
}

function targetFromConversation(conversationId: string): { kind: ConversationKind; target: string } | null {
  const separator = conversationId.indexOf(':')
  if (separator < 1) return null
  const kind = conversationId.slice(0, separator)
  const target = conversationId.slice(separator + 1)
  return (kind === 'private' || kind === 'group') && target ? { kind, target } : null
}

const activeTarget = computed(() => targetFromConversation(activeConversationId.value))
const conversationMessages = computed(() => messages.value
  .filter((message) => conversationIdForMessage(message) === activeConversationId.value)
  .slice()
  .sort((left, right) => left.createdAt - right.createdAt))

const conversations = computed<Conversation[]>(() => {
  const groupIds = new Set(groups.value)
  const privateIds = new Set<string>()
  messages.value.forEach((message) => {
    if (message.groupType === 'group') groupIds.add(message.to)
    else privateIds.add(message.direction === 'received' ? (message.from ?? message.to) : message.to)
  })
  const active = activeTarget.value
  if (active?.kind === 'private') privateIds.add(active.target)
  if (active?.kind === 'group') groupIds.add(active.target)

  const all = [
    ...[...privateIds].filter(Boolean).map((id) => makeConversation('private', id)),
    ...[...groupIds].filter(Boolean).map((id) => makeConversation('group', id)),
  ]
  return all.sort((left, right) => right.updatedAt - left.updatedAt || left.title.localeCompare(right.title, 'zh-CN'))
})

const activeConversation = computed(() => conversations.value.find((item) => item.id === activeConversationId.value) ?? null)
const canSend = computed(() => Boolean(activeTarget.value && content.value.trim() && !sending.value))

function makeConversation(kind: ConversationKind, target: string): Conversation {
  const id = kind === 'group' ? groupConversationId(target) : privateConversationId(target)
  const latest = messages.value
    .filter((message) => conversationIdForMessage(message) === id)
    .slice()
    .sort((left, right) => right.createdAt - left.createdAt)[0]
  return {
    id,
    kind,
    title: kind === 'group' ? target : `用户 #${target}`,
    updatedAt: latest?.createdAt ?? 0,
    preview: latest?.content ?? (kind === 'group' ? '群组实时对话' : '开始一段新对话'),
    unread: unreadByConversation.value[id] ?? 0,
  }
}

function persistConversationState() {
  sessionStorage.setItem(HISTORY_KEY, JSON.stringify(messages.value.slice(-MAX_MESSAGES)))
  sessionStorage.setItem(PENDING_ACK_KEY, JSON.stringify(pendingAcknowledgements.value))
}

function selectConversation(conversationId: string) {
  activeConversationId.value = conversationId
  if (unreadByConversation.value[conversationId]) {
    const next = { ...unreadByConversation.value }
    delete next[conversationId]
    unreadByConversation.value = next
  }
}

function startPrivateConversation() {
  const userId = newPeerId.value.trim()
  if (!userId) return
  selectConversation(privateConversationId(userId))
  newPeerId.value = ''
}

function chatWebSocketURL(): string {
  const base = API_BASE || window.location.origin
  const url = new URL('/api/v1/protected/chat/ws', base)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

function enqueueAcknowledgement(deliveryId: string) {
  if (pendingAcknowledgements.value.includes(deliveryId)) return
  pendingAcknowledgements.value = [...pendingAcknowledgements.value, deliveryId]
  persistConversationState()
}

async function flushAcknowledgements() {
  if (acknowledgementInFlight || !pendingAcknowledgements.value.length) return
  acknowledgementInFlight = true
  try {
    for (const deliveryId of [...pendingAcknowledgements.value]) {
      try {
        await chatApi.acknowledge(deliveryId)
        pendingAcknowledgements.value = pendingAcknowledgements.value.filter((id) => id !== deliveryId)
      } catch {
        // Keep it for the next reconnect/online event. Acknowledgement is idempotent.
        break
      }
    }
  } finally {
    acknowledgementInFlight = false
    persistConversationState()
  }
}

function isKnownDelivery(deliveryId: string): boolean {
  return messages.value.some((message) => message.deliveryIds.includes(deliveryId))
}

function isGroupEchoOfSentMessage(groupId: string, body: string, timestamp: number): SentMessage | undefined {
  return messages.value.find((message) =>
    message.direction === 'sent'
    && message.groupType === 'group'
    && message.to === groupId
    && message.content === body
    && Math.abs(message.createdAt - timestamp) < 30,
  )
}

function receiveMessage(raw: string) {
  try {
    const frame = JSON.parse(raw) as {
      metadata?: { deliveryId?: string; type?: string; groupType?: ConversationKind; from?: string; to?: string; timestamp?: number }
      content?: string
    }
    const metadata = frame.metadata
    if (
      metadata?.type !== 'text'
      || (metadata.groupType !== 'private' && metadata.groupType !== 'group')
      || !metadata.from
      || !metadata.to
      || typeof frame.content !== 'string'
    ) return

    const deliveryId = metadata.deliveryId
    if (deliveryId) enqueueAcknowledgement(deliveryId)
    if (deliveryId && isKnownDelivery(deliveryId)) {
      void flushAcknowledgements()
      return
    }

    const receivedAt = metadata.timestamp || Math.floor(Date.now() / 1000)
    const echoedMessage = metadata.groupType === 'group' ? isGroupEchoOfSentMessage(metadata.to, frame.content, receivedAt) : undefined
    if (echoedMessage) {
      if (deliveryId && !echoedMessage.deliveryIds.includes(deliveryId)) {
        messages.value = messages.value.map((message) => message.localId === echoedMessage.localId
          ? { ...message, deliveryIds: [...message.deliveryIds, deliveryId] }
          : message)
      }
      persistConversationState()
      void flushAcknowledgements()
      return
    }

    const incoming: SentMessage = {
      localId: deliveryId ?? crypto.randomUUID(),
      direction: 'received',
      from: metadata.from,
      to: metadata.to,
      groupType: metadata.groupType,
      content: frame.content,
      createdAt: receivedAt,
      deliveryIds: deliveryId ? [deliveryId] : [],
      status: 'received',
    }
    const conversationId = conversationIdForMessage(incoming)
    messages.value = [...messages.value, incoming].slice(-MAX_MESSAGES)
    if (conversationId !== activeConversationId.value) {
      unreadByConversation.value = {
        ...unreadByConversation.value,
        [conversationId]: (unreadByConversation.value[conversationId] ?? 0) + 1,
      }
    }
    persistConversationState()
    void flushAcknowledgements()
  } catch {
    // Ignore malformed frames. The server emits only chat wire JSON frames.
  }
}

const { connectionState, connect, retryNow } = useChatSocket({
  getURL: chatWebSocketURL,
  ensureSession: () => session.restore(true),
  onMessage: receiveMessage,
  onConnected: () => { void flushAcknowledgements() },
  onUnauthenticated: () => {
    toast.show({ tone: 'info', title: '会话已失效', message: '实时连接已安全关闭，请重新登录。' })
    void router.replace({ name: 'login', query: { redirect: '/app/chat' } })
  },
})

const connectionLabel: Record<ChatConnectionState, string> = {
  connecting: '正在建立安全连接',
  connected: '实时连接已建立',
  reconnecting: '正在恢复连接',
  disconnected: '会话已断开',
}

async function loadGroups() {
  groupsLoading.value = true
  try {
    groups.value = await chatApi.myGroups()
  } catch (error) {
    toast.show({ tone: 'error', title: '无法加载群组', message: getApiError(error).message })
  } finally {
    groupsLoading.value = false
  }
}

async function loadMembers() {
  const target = activeTarget.value
  if (target?.kind !== 'group') {
    members.value = []
    return
  }
  membersLoading.value = true
  try {
    members.value = await chatApi.members(target.target)
  } catch (error) {
    toast.show({ tone: 'error', title: '无法加载成员', message: getApiError(error).message })
  } finally {
    membersLoading.value = false
  }
}

async function joinGroup() {
  const groupId = groupIdToJoin.value.trim()
  if (!groupId) return
  joining.value = true
  try {
    const result = await chatApi.join(groupId)
    groupIdToJoin.value = ''
    await loadGroups()
    selectConversation(groupConversationId(result.groupId))
    toast.show({ tone: 'success', title: `已加入群组 ${result.groupId}`, message: result.supplement.length ? `已收到 ${result.supplement.length} 条窗口消息` : '现在可以开始群组实时对话。' })
  } catch (error) {
    toast.show({ tone: 'error', title: '加入失败', message: getApiError(error).message })
  } finally {
    joining.value = false
  }
}

async function leaveGroup(groupId: string) {
  try {
    await chatApi.leave(groupId)
    groups.value = groups.value.filter((id) => id !== groupId)
    if (activeConversationId.value === groupConversationId(groupId)) selectConversation('')
    toast.show({ tone: 'success', title: `已退出群组 ${groupId}` })
  } catch (error) {
    toast.show({ tone: 'error', title: '退出失败', message: getApiError(error).message })
  }
}

async function sendMessage(message: SentMessage) {
  try {
    const response = await chatApi.send({
      metadata: { type: 'text', groupType: message.groupType, to: message.to },
      content: message.content,
    })
    messages.value = messages.value.map((item) => item.localId === message.localId
      ? { ...item, deliveryIds: response.deliveryIds, status: 'accepted' }
      : item)
  } catch (error) {
    const apiError = getApiError(error)
    errorMessage.value = apiError.message
    messages.value = messages.value.map((item) => item.localId === message.localId ? { ...item, status: 'failed' } : item)
  } finally {
    persistConversationState()
  }
}

async function sendActiveMessage() {
  const target = activeTarget.value
  const body = content.value.trim()
  if (!target || !body || sending.value) return
  sending.value = true
  errorMessage.value = ''
  const message: SentMessage = {
    localId: crypto.randomUUID(),
    direction: 'sent',
    to: target.target,
    groupType: target.kind,
    content: body,
    createdAt: Math.floor(Date.now() / 1000),
    deliveryIds: [],
    status: 'sending',
  }
  messages.value = [...messages.value, message].slice(-MAX_MESSAGES)
  content.value = ''
  persistConversationState()
  await sendMessage(message)
  sending.value = false
}

async function retryMessage(message: SentMessage) {
  if (message.status !== 'failed' || sending.value) return
  sending.value = true
  errorMessage.value = ''
  messages.value = messages.value.map((item) => item.localId === message.localId ? { ...item, status: 'sending' } : item)
  await sendMessage({ ...message, status: 'sending' })
  sending.value = false
}

function restoreLocalConversationState() {
  try {
    const storedMessages = JSON.parse(sessionStorage.getItem(HISTORY_KEY) ?? '[]')
    if (Array.isArray(storedMessages)) messages.value = storedMessages.filter(isChatMessage).slice(-MAX_MESSAGES)
    const storedAcknowledgements = JSON.parse(sessionStorage.getItem(PENDING_ACK_KEY) ?? '[]')
    if (Array.isArray(storedAcknowledgements)) pendingAcknowledgements.value = storedAcknowledgements.filter((id): id is string => typeof id === 'string')
  } catch {
    messages.value = []
    pendingAcknowledgements.value = []
  }
}

function isChatMessage(value: unknown): value is SentMessage {
  if (!value || typeof value !== 'object') return false
  const message = value as Partial<SentMessage>
  return typeof message.localId === 'string'
    && (message.direction === 'sent' || message.direction === 'received')
    && (message.groupType === 'private' || message.groupType === 'group')
    && typeof message.to === 'string'
    && typeof message.content === 'string'
    && typeof message.createdAt === 'number'
    && Array.isArray(message.deliveryIds)
    && (message.status === 'sending' || message.status === 'accepted' || message.status === 'failed' || message.status === 'received')
}

function handleNetworkReturn() {
  retryNow()
  void flushAcknowledgements()
}

watch(activeConversationId, () => { void loadMembers() })

onMounted(() => {
  restoreLocalConversationState()
  void loadGroups()
  void flushAcknowledgements()
  connect()
  window.addEventListener('online', handleNetworkReturn)
})

onBeforeUnmount(() => window.removeEventListener('online', handleNetworkReturn))
</script>

<template>
  <div class="page chat-page">
    <header class="page-header chat-page__header">
      <div><span class="page-kicker">REAL-TIME MESSAGING</span><h1><MessageCircle :size="27" />消息</h1><p>通过安全的 HttpOnly Cookie 建立实时收件通道。</p></div>
      <div class="connection-chip" :class="{ 'connection-chip--limited': connectionState !== 'connected' }"><Radio :size="15" /><span>{{ connectionLabel[connectionState] }}</span><button v-if="connectionState !== 'connected'" type="button" class="icon-button" aria-label="立即重连" @click="retryNow"><RefreshCw :size="15" /></button></div>
    </header>

    <aside class="capability-notice chat-page__notice"><CircleAlert :size="20" /><div><strong>实时接收 · 可靠投递</strong><p>收到消息后，页面会先写入对话状态，再用 CSRF 保护的请求确认投递。发送使用可返回 delivery ID 的可靠接口，已连接的对方会即时收到消息。</p></div></aside>

    <div class="realtime-chat">
      <aside class="conversation-rail card">
        <div class="conversation-rail__heading"><div><span>INBOX</span><h2>对话</h2></div><span>{{ conversations.length }}</span></div>
        <form class="new-chat-form" @submit.prevent="startPrivateConversation"><input v-model.trim="newPeerId" aria-label="接收用户 ID" placeholder="输入用户 ID，开始私聊" /><button type="submit" class="icon-button" aria-label="创建私聊"><Plus :size="18" /></button></form>
        <div class="conversation-rail__list">
          <p v-if="groupsLoading" class="conversation-rail__loading">正在同步群组…</p>
          <template v-else-if="conversations.length"><div v-for="conversation in conversations" :key="conversation.id" class="conversation-row"><button type="button" class="conversation-item" :class="{ active: activeConversationId === conversation.id }" @click="selectConversation(conversation.id)"><span class="conversation-item__avatar" :class="`conversation-item__avatar--${conversation.kind}`"><Users v-if="conversation.kind === 'group'" :size="17" /><MessageCircle v-else :size="17" /></span><span class="conversation-item__content"><strong>{{ conversation.title }}</strong><small>{{ conversation.preview }}</small></span><span class="conversation-item__meta"><time>{{ conversation.updatedAt ? formatRelativeDate(conversation.updatedAt) : '' }}</time><i v-if="conversation.unread">{{ conversation.unread > 9 ? '9+' : conversation.unread }}</i></span></button><button v-if="conversation.kind === 'group'" type="button" class="conversation-row__leave" :aria-label="`退出群组 ${conversation.title}`" @click="leaveGroup(conversation.title)"><LogOut :size="14" /></button></div></template>
          <EmptyState v-else title="开始一段对话" description="输入用户 ID 发起私聊，或加入一个预置群组。" />
        </div>
        <form class="join-group-form" @submit.prevent="joinGroup"><span><UserPlus :size="15" />加入预置群组</span><div><input v-model.trim="groupIdToJoin" aria-label="预置群组 ID" placeholder="群组 ID" /><button type="submit" class="button button--soft button--small" :disabled="joining">{{ joining ? '加入中' : '加入' }}</button></div></form>
      </aside>

      <section class="thread card">
        <template v-if="activeConversation && activeTarget">
          <header class="thread__header"><div><span class="thread__eyebrow">{{ activeConversation.kind === 'group' ? 'GROUP CONVERSATION' : 'PRIVATE CONVERSATION' }}</span><h2>{{ activeConversation.title }}</h2></div><div v-if="activeConversation.kind === 'group'" class="thread__members"><Users :size="16" /><span>{{ membersLoading ? '加载成员…' : `${members.length} 位成员` }}</span><button type="button" class="icon-button" aria-label="刷新成员" @click="loadMembers"><RefreshCw :size="15" /></button></div></header>
          <div class="thread__messages" aria-live="polite"><div v-if="!conversationMessages.length" class="thread__blank"><MessageCircle :size="30" /><p>这段对话刚刚开始。说点什么吧。</p></div><article v-for="message in conversationMessages" :key="message.localId" class="message-bubble" :class="[`message-bubble--${message.direction}`, { 'message-bubble--failed': message.status === 'failed' }]"><span v-if="message.direction === 'received'" class="message-bubble__sender">用户 #{{ message.from }}</span><p>{{ message.content }}</p><footer><time>{{ formatRelativeDate(message.createdAt) }}</time><span v-if="message.status === 'sending'">发送中</span><span v-else-if="message.status === 'accepted'">已接受</span><span v-else-if="message.status === 'received' && message.deliveryIds.some((id) => pendingAcknowledgements.includes(id))">等待确认</span><span v-else-if="message.status === 'received'"><CheckCheck :size="13" />已确认</span><button v-else type="button" @click="retryMessage(message)">重试</button></footer></article></div>
          <form class="thread__composer" @submit.prevent="sendActiveMessage"><textarea v-model="content" rows="3" maxlength="49152" :placeholder="`发送给 ${activeConversation.title}`" @keydown.ctrl.enter.prevent="sendActiveMessage" /><div><span>{{ content.length.toLocaleString() }} / 49,152 · Ctrl + Enter 发送</span><button type="submit" class="button button--primary" :disabled="!canSend"><Send :size="17" />{{ sending ? '发送中…' : '发送' }}</button></div><p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p></form>
        </template>
        <EmptyState v-else title="选择一个对话" description="从左侧继续已有对话，或输入用户 ID 新建私聊。"><WifiOff :size="20" /></EmptyState>
      </section>
    </div>
  </div>
</template>
