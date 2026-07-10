<template>
  <div>
    <div class="app-page">
      <!-- 顶部导航 -->
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-check text-warning mr-2" />文章审核</h3>
        <router-link to="/reviews" class="btn btn-outline-secondary btn-sm">
          <i class="icon-reply" /> 返回列表
        </router-link>
      </div>

      <!-- 加载 / 错误 -->
      <div v-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
      <div v-if="loading" class="text-center py-5">
        <div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载中...</p>
      </div>

      <template v-if="!loading && article">
        <!-- 文章内容 — flex:1 撑满，内部滚动 -->
        <div class="card mb-3 review-article-card">
          <div class="card-header d-flex justify-content-between align-items-center">
            <div>
              <h4 class="mb-0">{{ article.title }}</h4>
              <small class="text-muted">更新时间：{{ fmt(article.updatedAt) }}</small>
            </div>
            <MarkdownThemeSwitch />
          </div>
          <div class="card-body" @click="handleAnchor">
            <div class="markdown-body" v-html="renderedContent" />
          </div>
        </div>

        <!-- 审核信息 + 操作：底部固定 -->
        <div class="review-footer">
          <div class="card mb-3">
            <div class="card-body py-2">
              <div class="d-flex align-items-center flex-wrap gap-3">
                <span><strong>状态：</strong><span class="badge" :class="statusBadgeClass">{{ article.reviewStatus || 'pending' }}</span></span>
                <span><strong>审核人：</strong>{{ article.reviewerId || '-' }}</span>
                <span class="text-muted small">创建 {{ fmt(article.createdAt) }} · 更新 {{ fmt(article.updatedAt) }}</span>
              </div>
              <div v-if="article.reviewComment" class="mt-2">
                <strong>备注：</strong><span class="text-muted">{{ article.reviewComment }}</span>
              </div>
            </div>
          </div>

          <div class="card" v-if="!article.reviewStatus || article.reviewStatus === 'pending'">
            <div class="card-body py-2">
              <div class="d-flex align-items-center gap-2">
                <input v-model="comment" class="form-control form-control-sm" placeholder="审核备注（可选）..." style="flex:1" />
                <button class="btn btn-success btn-sm" :disabled="submitting" @click="handleReview('approved')">
                  <i class="icon-check" /> {{ submitting ? '...' : '通过' }}
                </button>
                <button class="btn btn-danger btn-sm" :disabled="submitting" @click="handleReview('rejected')">
                  <i class="icon-close" /> {{ submitting ? '...' : '驳回' }}
                </button>
              </div>
              <div v-if="submitError" class="alert alert-danger py-1 px-2 mt-2 mb-0 small">{{ submitError }}</div>
              <div v-if="submitSuccess" class="alert alert-success py-1 px-2 mt-2 mb-0 small">{{ submitSuccess }}</div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { markdownAPI } from '@/api/markdown'
import { fmtDate } from '@/utils/format'
import { renderMarkdown } from '@/utils/markdown'
import { useArticleRefresh } from '@/composables/useArticleRefresh'
import { MarkdownThemeSwitch } from '@/components'
import { notify } from '@/composables/useNotify'

const route = useRoute()
const article = ref(null)
const loading = ref(false)
const errorMsg = ref('')
const comment = ref('')
const submitting = ref(false)
const submitError = ref('')
const submitSuccess = ref('')

const renderedContent = computed(() => {
  if (!article.value?.content) return ''
  return renderMarkdown(article.value.content)
})

const statusBadgeClass = computed(() => {
  const s = article.value?.reviewStatus
  if (s === 'approved') return 'badge-success'
  if (s === 'rejected') return 'badge-danger'
  return 'badge-info'
})

const { trigger: refreshArticles } = useArticleRefresh()

onMounted(async () => {
  loading.value = true
  const id = route.params.id
  try {
    const r = await markdownAPI.preview(id)
    article.value = r.markdown
  } catch (e) {
    errorMsg.value = '加载失败：' + ((e.response && e.response.data && e.response.data.message) || '未知错误')
  } finally {
    loading.value = false
  }
})

async function handleReview(status) {
  submitError.value = ''
  submitSuccess.value = ''
  submitting.value = true
  try {
    await markdownAPI.review.submit(route.params.id, {
      status,
      comment: comment.value
    })
    submitSuccess.value = status === 'approved' ? '✅ 已通过审核' : '❌ 已驳回'
    // 后续刷新操作独立捕获，不影响审核结果
    try { refreshArticles() } catch { /* 刷新列表失败不影响 */ }
    try {
      const r = await markdownAPI.preview(route.params.id)
      article.value = r.markdown
    } catch { /* 刷新详情失败不影响 */ }
  } catch (e) {
    submitError.value = '操作失败：' + ((e.response && e.response.data && e.response.data.message) || '未知错误')
  } finally {
    submitting.value = false
  }
}

function fmt(ts) { return fmtDate(ts) }

// 拦截 markdown 内链接点击：锚点 → 页面内滚动；其余一律阻止默认行为并确认
function handleAnchor(e) {
  const a = e.target.closest('a')
  if (!a) return
  const href = a.getAttribute('href')
  if (!href) return

  if (href.startsWith('#')) {
    e.preventDefault()
    const el = document.getElementById(href.slice(1))
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    return
  }

  e.preventDefault()
  notify.confirmExternalLink(href).then(ok => {
    if (ok) window.open(href, '_blank', 'noopener,noreferrer')
  })
}
</script>

<style scoped>
/* ── 审核详情页布局：文章 flex:1 撑满 + 底栏固定 ── */
.review-article-card {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.review-article-card .card-body {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}
.review-footer {
  flex-shrink: 0;
}
.gap-2 { gap: .5rem; }
.gap-3 { gap: .75rem; }
</style>
