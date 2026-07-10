<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-doc-text text-primary mr-2" />文章中心</h3>
        <div>
          <button class="btn btn-outline-secondary btn-sm mr-2" @click="refresh"><i class="icon-arrows-cw" /> 刷新</button>
          <router-link v-if="auth.isContentUser" to="/articles/new" class="btn btn-primary btn-sm"><i class="icon-plus" /> 发布文章</router-link>
        </div>
      </div>

      <!-- 加载 / 错误 -->
      <div v-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
      <div v-if="loading" class="text-center py-5">
        <div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载中...</p>
      </div>

      <!-- 卡片列表（选中后隐藏） -->
      <template v-if="!selectedItem && !detailLoading && !loading">
        <div v-if="list.length === 0" class="text-center py-5 text-muted">暂无文章</div>

        <div v-for="item in list" :key="item.markdownId" class="list-card mb-3" @click="selectItem(item)">
          <div class="list-card-body">
            <h5 class="list-card-title">{{ item.title }}</h5>
            <p class="list-card-summary">{{ stripMd(item.summary) || '暂无摘要' }}</p>
            <div class="list-card-footer">
              <span class="list-card-time">
                {{ fmt(item.updatedAt) }}
                <span v-if="item.authorId === auth.userInfo?.userId" class="badge badge-success ml-2">我的</span>
                <span v-if="item.reviewStatus === 'approved'" class="badge badge-info ml-1">已审核</span>
              </span>
              <div v-if="canModify(item)" class="list-card-actions" @click.stop>
                  <router-link :to="`/articles/${item.markdownId}/edit`" class="btn btn-outline-primary btn-sm">编辑</router-link>
                  <button @click="del(item)" class="btn btn-outline-danger btn-sm">删除</button>
              </div>
            </div>
          </div>
        </div>

        <!-- 分页 -->
        <div v-if="totalPages > 1" class="d-flex justify-content-center mt-4">
          <button class="btn btn-sm btn-default" :disabled="page <= 1" @click="page--">上一页</button>
          <span class="mx-3 align-self-center text-muted">第 {{ page }} / {{ totalPages }} 页</span>
          <button class="btn btn-sm btn-default" :disabled="page >= totalPages" @click="page++">下一页</button>
        </div>
      </template>

      <!-- 详情加载中 -->
      <div v-if="detailLoading" class="text-center py-5">
        <div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载详情...</p>
      </div>

      <!-- 详情卡片（选中某篇文章后展示） -->
      <div v-if="selectedItem && !detailLoading" class="detail-card">
        <div class="detail-card-header">
          <button class="btn btn-outline-secondary btn-sm" @click="selectedItem = null">
            <i class="icon-reply" /> 返回列表
          </button>
          <MarkdownThemeSwitch />
        </div>
        <div class="detail-card-body" @click="handleAnchor">
          <h4>{{ selectedItem.title }}</h4>
          <p class="text-muted mb-3">更新时间：{{ fmt(selectedItem.updatedAt) }}</p>
          <hr />
          <div class="markdown-body" v-html="renderedContent" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { markdownAPI } from '@/api/markdown'
import { usePagination } from '@/composables/usePagination'
import { useAsync } from '@/composables/useAsync'
import { useArticleRefresh } from '@/composables/useArticleRefresh'
import { fmtDate, stripMd } from '@/utils/format'
import { notify } from '@/composables/useNotify'
import { userMsg } from '@/utils/error'
import { renderMarkdown } from '@/utils/markdown'
import { MarkdownThemeSwitch } from '@/components'

const auth = useAuthStore()
const list = ref([])
const selectedItem = ref(null)
const detailLoading = ref(false)
const { loading, errorMsg, run } = useAsync()

const renderedContent = computed(() => {
  if (!selectedItem.value?.content) return ''
  return renderMarkdown(selectedItem.value.content)
})

async function selectItem(item) {
  detailLoading.value = true
  try {
    const r = await markdownAPI.preview(item.markdownId)
    selectedItem.value = r.markdown || null
  } catch (e) {
    notify.error(userMsg(e, '加载文章详情'))
  } finally {
    detailLoading.value = false
  }
}

async function fetchData(p) {
  const pg = p ?? page.value
  await run(async () => {
    const r = await markdownAPI.list({ page: pg, pageSize: pageSize.value })
    list.value = r.markdownList?.markdownList || []
    totalCount.value = r.totalCount || 0
  }, '加载文章失败')
}

/** 刷新列表：重新请求当前页，不重置页码 */
async function refresh() {
  selectedItem.value = null
  await fetchData(page.value)
}

const { page, pageSize, totalCount, totalPages } = usePagination(fetchData)

// 监听审核通过事件：审核完成后自动回到第一页刷新
const { tick } = useArticleRefresh()
watch(tick, () => {
  selectedItem.value = null
  page.value = 1
})

async function del(item) {
  const ok = await notify.confirmDelete(`确定删除文章「${item.title}」吗？`)
  if (!ok) return
  try {
    await markdownAPI.delete(item.markdownId)
    selectedItem.value = null
    await fetchData()
    notify.success('删除成功')
  } catch (e) {
    notify.error(userMsg(e, '删除'))
  }
}

function canModify(item) {
  return auth.userInfo?.userId === item.authorId
}

// 拦截 markdown 内链接点击：锚点 → 卡片内滚动；其余一律阻止默认行为并确认
function handleAnchor(e) {
  const a = e.target.closest('a')
  if (!a) return
  const href = a.getAttribute('href')
  if (!href) return

  // 内部锚点：卡片内滚动
  if (href.startsWith('#')) {
    e.preventDefault()
    const el = document.getElementById(href.slice(1))
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    return
  }

  // 其余所有链接（外部 / 相对路径 / 未知）：阻止跳转，弹窗确认后新标签页打开
  e.preventDefault()
  notify.confirmExternalLink(href).then(ok => {
    if (ok) window.open(href, '_blank', 'noopener,noreferrer')
  })
}

function fmt(ts) { return fmtDate(ts) }
</script>

<style scoped>
/* ── 详情卡片 ── */
.detail-card {
  background: var(--app-bg-card);
  border: 1px solid var(--app-border);
  border-radius: 8px;
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

.detail-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 24px;
  border-bottom: 2px solid var(--app-brand);
  flex-shrink: 0;
}

.detail-card-body {
  padding: 24px;
  text-align: left;
  color: var(--app-text-primary);
  overflow-y: auto;
  flex: 1;
  min-height: 0;
  border-radius: 0 0 6px 6px;

  h4 {
    color: var(--app-brand);
    margin-bottom: 8px;
  }

  hr {
    border-color: var(--app-border);
  }
}
</style>

<style>
/* markdown-body 基础样式（v-html 内容，不可 scoped） */
.markdown-body {
  padding: 10px;
  border-radius: 6px;
  box-sizing: border-box;
}
/* 对抗 Bootstrap h1-h6 { color: #333 }，github-markdown-css 靠继承设色 */
.markdown-body h1,
.markdown-body h2,
.markdown-body h3,
.markdown-body h4,
.markdown-body h5,
.markdown-body h6 {
  color: inherit;
}
/* 对抗全局 code { color: $pink }，github-markdown-css + hljs 靠继承/span 设色 */
.markdown-body code {
  color: inherit;
  background-color: inherit;
}
</style>
