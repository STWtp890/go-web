<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { CheckCheck, CircleAlert, LogOut, MessageCircle, Radio, RefreshCw, Send, UserPlus, Users } from '@lucide/vue'
import { chatApi } from '@/api/chat'
import { getApiError } from '@/api/client'
import EmptyState from '@/components/EmptyState.vue'
import { useToastStore } from '@/stores/toast'
import type { SentMessage } from '@/types/domain'
import { formatRelativeDate } from '@/utils/format'

const HISTORY_KEY = 'paperplane:sent-message-history'
const toast = useToastStore()

const mode = ref<'private' | 'group'>('private')
const recipient = ref('')
const content = ref('')
const sending = ref(false)
const errorMessage = ref('')
const messages = ref<SentMessage[]>([])

const groups = ref<string[]>([])
const groupsLoading = ref(false)
const groupIdToJoin = ref('')
const joining = ref(false)
const selectedGroup = ref('')
const members = ref<string[]>([])
const membersLoading = ref(false)

const currentTarget = computed(() => mode.value === 'group' ? selectedGroup.value : recipient.value.trim())
const canSend = computed(() => Boolean(currentTarget.value && content.value.trim() && !sending.value))
const visibleMessages = computed(() => messages.value.filter((message) => message.groupType === mode.value))

function persistMessages() {
  sessionStorage.setItem(HISTORY_KEY, JSON.stringify(messages.value.slice(0, 50)))
}

async function loadGroups() {
  groupsLoading.value = true
  try {
    groups.value = await chatApi.myGroups()
    if (selectedGroup.value && !groups.value.includes(selectedGroup.value)) selectedGroup.value = ''
    if (!selectedGroup.value && groups.value.length) selectedGroup.value = groups.value[0] ?? ''
  } catch (error) {
    toast.show({ tone: 'error', title: '无法加载群组', message: getApiError(error).message })
  } finally {
    groupsLoading.value = false
  }
}

async function loadMembers() {
  if (!selectedGroup.value) {
    members.value = []
    return
  }
  membersLoading.value = true
  try { members.value = await chatApi.members(selectedGroup.value) }
  catch (error) { toast.show({ tone: 'error', title: '无法加载成员', message: getApiError(error).message }) }
  finally { membersLoading.value = false }
}

async function joinGroup() {
  const groupId = groupIdToJoin.value.trim()
  if (!groupId) return
  joining.value = true
  try {
    const result = await chatApi.join(groupId)
    groupIdToJoin.value = ''
    selectedGroup.value = result.groupId
    await loadGroups()
    toast.show({ tone: 'success', title: `已加入群组 ${result.groupId}`, message: result.supplement.length ? `收到 ${result.supplement.length} 条窗口补发` : '现在可以发送群消息了' })
  } catch (error) {
    toast.show({ tone: 'error', title: '加入失败', message: getApiError(error).message })
  } finally { joining.value = false }
}

async function leaveGroup(groupId: string) {
  try {
    await chatApi.leave(groupId)
    selectedGroup.value = ''
    members.value = []
    await loadGroups()
    toast.show({ tone: 'success', title: `已退出群组 ${groupId}` })
  } catch (error) {
    toast.show({ tone: 'error', title: '退出失败', message: getApiError(error).message })
  }
}

async function sendMessage() {
  if (!canSend.value) return
  const messageContent = content.value.trim()
  const target = currentTarget.value
  sending.value = true
  errorMessage.value = ''
  try {
    const response = await chatApi.send({
      metadata: { type: 'text', groupType: mode.value, to: target },
      content: messageContent,
    })
    messages.value.unshift({
      localId: crypto.randomUUID(), to: target, groupType: mode.value, content: messageContent,
      createdAt: Math.floor(Date.now() / 1000), deliveryIds: response.deliveryIds, status: 'accepted',
    })
    persistMessages()
    content.value = ''
    toast.show({ tone: 'success', title: '消息已被服务端接受', message: `生成 ${response.deliveryIds.length} 个投递记录` })
  } catch (error) {
    const apiError = getApiError(error)
    errorMessage.value = apiError.message
    messages.value.unshift({
      localId: crypto.randomUUID(), to: target, groupType: mode.value, content: messageContent,
      createdAt: Math.floor(Date.now() / 1000), deliveryIds: [], status: 'failed',
    })
    persistMessages()
  } finally { sending.value = false }
}

watch(selectedGroup, () => { void loadMembers() })
onMounted(() => {
  try { messages.value = JSON.parse(sessionStorage.getItem(HISTORY_KEY) ?? '[]') as SentMessage[] } catch { messages.value = [] }
  void loadGroups()
})
</script>

<template>
  <div class="page chat-page">
    <header class="page-header">
      <div><span class="page-kicker">MESSAGING</span><h1><MessageCircle :size="27" />消息中心</h1><p>发送私信或群消息，管理你已加入的群组。</p></div>
      <div class="connection-chip connection-chip--limited"><Radio :size="15" /><span>HTTP 通道可用</span></div>
    </header>

    <aside class="capability-notice">
      <CircleAlert :size="20" />
      <div><strong>实时收件暂不可用</strong><p>当前后端要求在 WS/SSE 握手中携带 Authorization，而浏览器原生接口无法设置该请求头。这里仅展示本次浏览器会话内的发送记录，不会伪装成完整聊天历史。</p></div>
    </aside>

    <div class="chat-layout">
      <section class="chat-compose card">
        <div class="segmented-control" aria-label="消息类型">
          <button type="button" :class="{ active: mode === 'private' }" @click="mode = 'private'">私信</button>
          <button type="button" :class="{ active: mode === 'group' }" @click="mode = 'group'">群消息</button>
        </div>

        <form class="message-form" @submit.prevent="sendMessage">
          <label v-if="mode === 'private'" class="field"><span>接收用户 ID</span><input v-model.trim="recipient" placeholder="例如：12" required /></label>
          <label v-else class="field"><span>目标群组</span><select v-model="selectedGroup" required><option value="" disabled>选择已加入的群组</option><option v-for="group in groups" :key="group" :value="group">{{ group }}</option></select></label>
          <label class="field"><span>消息内容 <small>{{ content.length.toLocaleString() }} / 49,152 bytes</small></span><textarea v-model="content" rows="7" maxlength="49152" placeholder="输入一条文本消息…" required /></label>
          <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
          <button type="submit" class="button button--primary button--full" :disabled="!canSend"><Send :size="17" />{{ sending ? '正在发送…' : '发送消息' }}</button>
        </form>

        <div v-if="mode === 'group' && selectedGroup" class="member-panel">
          <div><span><Users :size="16" />群组成员</span><button type="button" class="icon-button" aria-label="刷新成员" @click="loadMembers"><RefreshCw :size="15" /></button></div>
          <p v-if="membersLoading">正在加载…</p>
          <div v-else class="member-list"><span v-for="member in members" :key="member">#{{ member }}</span><small v-if="!members.length">暂无成员</small></div>
        </div>
      </section>

      <section class="chat-history card">
        <div class="card-heading"><div><span>SENT THIS SESSION</span><h2>发送记录</h2></div><span>{{ visibleMessages.length }} 条</span></div>
        <div v-if="visibleMessages.length" class="message-list">
          <article v-for="message in visibleMessages" :key="message.localId" class="sent-message" :class="{ 'sent-message--failed': message.status === 'failed' }">
            <div class="sent-message__meta"><span>发往 {{ message.groupType === 'group' ? '群组' : '用户' }} <strong>#{{ message.to }}</strong></span><span>{{ formatRelativeDate(message.createdAt) }}</span></div>
            <p>{{ message.content }}</p>
            <footer><CheckCheck v-if="message.status === 'accepted'" :size="15" /><CircleAlert v-else :size="15" />{{ message.status === 'accepted' ? `服务端已接受 · ${message.deliveryIds.length} 个投递 ID` : '发送失败' }}</footer>
          </article>
        </div>
        <EmptyState v-else title="还没有发送记录" description="本页只保留当前浏览器会话内发出的消息。" />
      </section>

      <section class="group-manager card">
        <div class="card-heading"><div><span>YOUR GROUPS</span><h2>我的群组</h2></div><button type="button" class="icon-button" aria-label="刷新群组" @click="loadGroups"><RefreshCw :size="16" /></button></div>
        <form class="join-form" @submit.prevent="joinGroup"><input v-model.trim="groupIdToJoin" placeholder="输入预置群组 ID" aria-label="群组 ID" /><button class="button button--soft" type="submit" :disabled="joining"><UserPlus :size="16" />{{ joining ? '加入中' : '加入' }}</button></form>
        <p v-if="groupsLoading" class="muted">正在加载群组…</p>
        <div v-else-if="groups.length" class="group-list">
          <button v-for="group in groups" :key="group" type="button" :class="{ active: selectedGroup === group }" @click="mode = 'group'; selectedGroup = group">
            <span><Users :size="17" /><strong>{{ group }}</strong></span>
            <span class="group-list__actions"><small>{{ selectedGroup === group ? '当前' : '选择' }}</small><i role="button" tabindex="0" title="退出群组" @click.stop="leaveGroup(group)" @keydown.enter.stop="leaveGroup(group)"><LogOut :size="14" /></i></span>
          </button>
        </div>
        <p v-else class="muted">尚未加入任何群组。群组需由应用或管理员预置。</p>
      </section>
    </div>
  </div>
</template>
