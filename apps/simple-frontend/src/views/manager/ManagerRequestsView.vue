<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Check, ChevronRight, Clock3, Inbox, Mail, RefreshCw, ShieldCheck, User, X } from '@lucide/vue'
import { managerApi } from '@/api/manager'
import { getApiError } from '@/api/client'
import EmptyState from '@/components/EmptyState.vue'
import PaginationControl from '@/components/PaginationControl.vue'
import { useToastStore } from '@/stores/toast'
import type { ApiMeta } from '@/types/api'
import type { RegistrationRequest } from '@/types/domain'
import { formatDate } from '@/utils/format'

const toast = useToastStore()
const status = ref<RegistrationRequest['status']>('pending')
const requests = ref<RegistrationRequest[]>([])
const meta = ref<ApiMeta>({ page: 1, per_page: 10, total: 0, total_pages: 0 })
const loading = ref(false)
const errorMessage = ref('')
const selected = ref<RegistrationRequest | null>(null)
const reviewAction = ref<'approve' | 'reject'>('approve')
const reviewComment = ref('')
const reviewing = ref(false)

const tabs: Array<{ value: RegistrationRequest['status']; label: string }> = [
  { value: 'pending', label: '待审批' }, { value: 'approved', label: '已通过' }, { value: 'rejected', label: '已拒绝' },
]
const statusLabel = computed(() => tabs.find((tab) => tab.value === status.value)?.label ?? '')

async function load(page = 1) {
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await managerApi.requests(status.value, page)
    requests.value = result.items
    meta.value = result.meta
  } catch (error) { errorMessage.value = getApiError(error).message }
  finally { loading.value = false }
}

function changeStatus(value: RegistrationRequest['status']) {
  status.value = value
  selected.value = null
  void load(1)
}

function openReview(request: RegistrationRequest, action: 'approve' | 'reject') {
  selected.value = request
  reviewAction.value = action
  reviewComment.value = ''
}

async function submitReview() {
  if (!selected.value) return
  reviewing.value = true
  try {
    if (reviewAction.value === 'approve') await managerApi.approve(selected.value.id, reviewComment.value.trim())
    else await managerApi.reject(selected.value.id, reviewComment.value.trim())
    toast.show({ tone: 'success', title: reviewAction.value === 'approve' ? '申请已通过' : '申请已拒绝', message: `${selected.value.username} 的申请状态已更新` })
    selected.value = null
    await load(meta.value.page)
  } catch (error) {
    const apiError = getApiError(error)
    toast.show({ tone: 'error', title: '审批未完成', message: apiError.message })
    if (apiError.status === 409) {
      selected.value = null
      await load(meta.value.page)
    }
  } finally { reviewing.value = false }
}

onMounted(() => load())
</script>

<template>
  <div class="manager-page">
    <header class="manager-page__header">
      <div><span>ACCESS GOVERNANCE</span><h1>管理员申请审批</h1><p>核对申请资料，维护管理端的访问边界。</p></div>
      <button type="button" class="button button--soft" @click="load(meta.page)"><RefreshCw :size="16" />刷新列表</button>
    </header>

    <div class="manager-tabs" role="tablist">
      <button v-for="tab in tabs" :key="tab.value" type="button" role="tab" :aria-selected="status === tab.value" :class="{ active: status === tab.value }" @click="changeStatus(tab.value)">{{ tab.label }}</button>
    </div>

    <div class="request-summary"><span><Inbox :size="18" />{{ statusLabel }}</span><strong>{{ loading ? '—' : meta.total }} 项</strong></div>
    <p v-if="errorMessage" class="inline-error" role="alert">{{ errorMessage }} <button type="button" @click="load(meta.page)">重试</button></p>

    <div v-if="loading" class="request-list"><div v-for="n in 4" :key="n" class="request-skeleton" /></div>
    <div v-else-if="requests.length" class="request-list">
      <article v-for="request in requests" :key="request.id" class="request-card">
        <span class="request-card__avatar">{{ request.username.slice(0, 2).toUpperCase() }}</span>
        <div class="request-card__identity"><span>申请 #{{ request.id }}</span><h2>{{ request.username }}</h2><p v-if="request.email"><Mail :size="14" />{{ request.email }}</p></div>
        <div class="request-card__reason"><span>申请理由</span><p>{{ request.reason || '申请人未填写理由。' }}</p></div>
        <div class="request-card__time"><Clock3 :size="14" />{{ formatDate(request.createdAt) }}</div>
        <div v-if="request.status === 'pending'" class="request-card__actions">
          <button type="button" class="button button--danger-soft button--small" @click="openReview(request, 'reject')"><X :size="15" />拒绝</button>
          <button type="button" class="button button--success button--small" @click="openReview(request, 'approve')"><Check :size="15" />通过</button>
        </div>
        <div v-else class="request-card__reviewed">
          <span class="status-badge" :class="`status-badge--${request.status}`">{{ request.status === 'approved' ? '已通过' : '已拒绝' }}</span>
          <p v-if="request.reviewComment">{{ request.reviewComment }}</p>
        </div>
        <ChevronRight class="request-card__chevron" :size="18" />
      </article>
    </div>
    <EmptyState v-else :title="`没有${statusLabel}申请`" description="当前筛选条件下没有需要展示的记录。" />
    <PaginationControl :page="meta.page" :total-pages="meta.total_pages" :total="meta.total" @change="load" />

    <Teleport to="body">
      <Transition name="modal">
        <div v-if="selected" class="modal-backdrop" @click.self="selected = null">
          <section class="review-modal" role="dialog" aria-modal="true" aria-labelledby="review-title">
            <button type="button" class="icon-button review-modal__close" aria-label="关闭" @click="selected = null"><X :size="19" /></button>
            <span class="review-modal__icon" :class="{ 'review-modal__icon--reject': reviewAction === 'reject' }"><ShieldCheck v-if="reviewAction === 'approve'" :size="25" /><User v-else :size="25" /></span>
            <span class="page-kicker">REVIEW REQUEST #{{ selected.id }}</span>
            <h2 id="review-title">{{ reviewAction === 'approve' ? `通过 ${selected.username} 的申请？` : `拒绝 ${selected.username} 的申请？` }}</h2>
            <p>{{ reviewAction === 'approve' ? '通过后将原子创建一个可登录的管理员账号。' : '该申请会被标记为已拒绝，之后不能再次审批。' }}</p>
            <label class="field"><span>审批意见 <small>可选</small></span><textarea v-model="reviewComment" rows="4" maxlength="512" :placeholder="reviewAction === 'approve' ? '记录通过依据…' : '说明拒绝原因…'" /></label>
            <div class="review-modal__actions"><button type="button" class="button button--ghost" @click="selected = null">取消</button><button type="button" class="button" :class="reviewAction === 'approve' ? 'button--success' : 'button--danger'" :disabled="reviewing" @click="submitReview">{{ reviewing ? '正在提交…' : reviewAction === 'approve' ? '确认通过' : '确认拒绝' }}</button></div>
          </section>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
