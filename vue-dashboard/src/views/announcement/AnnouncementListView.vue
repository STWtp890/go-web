<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-volume-high text-primary mr-2" />公告管理</h3>
        <router-link v-if="auth.canManageAnno" to="/notices/new" class="btn btn-primary btn-sm"><i class="icon-plus" /> 新建公告</router-link>
      </div>

      <!-- 加载 / 错误 -->
      <div v-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
      <div v-if="loading" class="text-center py-5">
        <div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载中...</p>
      </div>

      <!-- 卡片列表（选中后隐藏） -->
      <template v-if="!selectedItem && !detailLoading && !loading">
        <div v-if="list.length === 0" class="text-center py-5 text-muted">暂无公告</div>

        <div v-for="item in list" :key="item.announcementId" class="list-card mb-3" @click="selectItem(item)">
          <div class="list-card-body">
            <h5 class="list-card-title">{{ item.title }}</h5>
            <div class="list-card-footer">
              <span class="list-card-time">{{ fmt(item.updatedAt) }}</span>
              <div v-if="auth.canManageAnno" class="list-card-actions" @click.stop>
                <router-link :to="`/notices/${item.announcementId}/edit`" class="btn btn-outline-primary btn-sm">编辑</router-link>
                <button @click="del(item)" class="btn btn-outline-danger btn-sm ml-1">删除</button>
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

      <!-- 详情卡片（选中某个公告后展示） -->
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
import { ref, computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { announcementAPI } from '@/api/announcement'
import { usePagination } from '@/composables/usePagination'
import { useAsync } from '@/composables/useAsync'
import { fmtDate } from '@/utils/format'
import { userMsg } from '@/utils/error'
import { marked } from 'marked'
import { notify } from '@/composables/useNotify'
import { MarkdownThemeSwitch } from '@/components'

const auth = useAuthStore()
const list = ref([])
const selectedItem = ref(null)
const detailLoading = ref(false)
const { loading, errorMsg, run } = useAsync()

const renderedContent = computed(() => {
  if (!selectedItem.value?.content) return ''
  return marked.parse(selectedItem.value.content)
})

async function selectItem(item) {
  detailLoading.value = true
  try {
    const r = await announcementAPI.preview(item.announcementId)
    selectedItem.value = r.announcement || null
  } catch (e) {
    notify.error(userMsg(e, '加载公告详情'))
  } finally {
    detailLoading.value = false
  }
}

async function fetchData(p) {
  const pg = p ?? page.value
  await run(async () => {
    const r = await announcementAPI.list({ page: pg, pageSize: pageSize.value })
    list.value = r.announcementList?.announcementList || []
    totalCount.value = r.totalCount || 0
  }, '加载公告失败')
}

const { page, pageSize, totalCount, totalPages } = usePagination(fetchData)

async function del(item) {
  const ok = await notify.confirmDelete(`确定删除公告「${item.title}」吗？`)
  if (!ok) return
  try {
    await announcementAPI.delete(item.announcementId)
    selectedItem.value = null
    await fetchData()
  } catch (e) {
    notify.error(userMsg(e, '删除'))
  }
}

function fmt(ts) { return fmtDate(ts) }

// 拦截 markdown 内链接点击：锚点 → 卡片内滚动；其余一律阻止默认行为并确认
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
/* ── 卡片容器 ── */
.list-card {
  background: var(--app-bg-card);
  border: 1px solid var(--app-border);
  border-radius: 8px;
  cursor: pointer;
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  transition: border-color .2s, box-shadow .2s;
}
.list-card:hover {
  border-color: var(--app-brand);
  box-shadow: 0 2px 12px rgba(0,0,0,.06);
}

.list-card-body {
  padding: 20px 24px;
}

/* ── 标题 — 品牌绿加粗 ── */
.list-card-title {
  color: var(--app-brand);
  font-weight: 700;
  font-size: 1.1rem;
  margin: 0 0 8px;
}

/* ── 摘要 — 灰色弱化 ── */
.list-card-summary {
  color: var(--app-text-muted);
  font-size: .9rem;
  line-height: 1.6;
  margin: 0 0 14px;
}

/* ── 底部栏：时间 + 操作 ── */
.list-card-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.list-card-time {
  color: var(--app-text-primary);
  font-size: .85rem;
  font-weight: 500;
}

.list-card-actions {
  display: flex;
  gap: 6px;
}

/* ── 详情卡片 ── */
.detail-card {
  background: var(--app-bg-card);
  border: 1px solid var(--app-border);
  border-radius: 8px;
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
}

.detail-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 24px;
  border-bottom: 2px solid var(--app-brand);
}

.detail-card-body {
  padding: 24px;
  text-align: left;
  color: var(--app-text-primary);

  h4 {
    color: var(--app-brand);
    margin-bottom: 8px;
  }

  hr {
    border-color: var(--app-border);
  }
}
</style>
