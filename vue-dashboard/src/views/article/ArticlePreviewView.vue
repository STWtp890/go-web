<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-doc-text text-primary mr-2" />{{ (article && article.title) || '文章阅读' }}</h3>
        <div>
          <router-link to="/articles" class="btn btn-outline-secondary btn-sm mr-2"><i class="icon-left-open" /> 返回列表</router-link>
          <router-link v-if="article && auth.userInfo?.userId === article.authorId" :to="`/articles/${$route.params.id}/edit`" class="btn btn-outline-primary btn-sm mr-2"><i class="icon-pencil" /> 编辑</router-link>
          <MarkdownThemeSwitch />
        </div>
      </div>
      <div v-if="loading" class="text-center py-5"><div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载中...</p></div>
      <div v-else-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
      <div v-else-if="article" class="card"><div class="card-body" @click="handleAnchor">
        <h2 class="mb-2">{{ article.title }}
          <span v-if="article.reviewStatus === 'pending'" class="badge badge-warning ml-2" style="font-size:.6em;vertical-align:middle">待审核</span>
          <span v-else-if="article.reviewStatus === 'rejected'" class="badge badge-danger ml-2" style="font-size:.6em;vertical-align:middle">已驳回</span>
        </h2>
        <p class="text-muted mb-4">更新时间：{{ fmt(article.updatedAt) }}</p><hr />
        <div class="markdown-body" v-html="rendered" />
      </div></div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { markdownAPI } from '@/api/markdown'
import { fmtDate } from '@/utils/format'
import { renderMarkdown } from '@/utils/markdown'
import { useMarkdownTheme } from '@/composables/useMarkdownTheme'
import { MarkdownThemeSwitch } from '@/components'
import { notify } from '@/composables/useNotify'

useMarkdownTheme() // 确保 CSS 已加载
const route = useRoute()
const auth = useAuthStore()
const article = ref(null)
const loading = ref(false)
const errorMsg = ref('')

const rendered = computed(() => {
  if (!article.value?.content) return ''
  return renderMarkdown(article.value.content)
})

// ── 初始加载（onMounted 内 await，无需 Suspense）──
onMounted(async () => {
  loading.value = true
  try {
    const r = await markdownAPI.preview(route.params.id)
    article.value = r.markdown
  } catch (e) {
    errorMsg.value = '加载失败：' + ((e.response && e.response.data && e.response.data.message) || '未知错误')
  } finally {
    loading.value = false
  }
})

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

<style lang="scss">
/* ── 仅保留布局微调，颜色/排版由 github-markdown-css 全权接管 ── */
.markdown-body {
  padding: 10px;
  border-radius: 6px;
  box-sizing: border-box;
  text-align: left;

  // 标题显式继承颜色（对抗 Bootstrap + $pink 全局 code 规则）
  h1, h2, h3, h4, h5, h6 {
    color: inherit;
    &:first-child { margin-top: 0; }
  }

  code {
    color: inherit;
    background-color: inherit;
  }

  // 代码块容器圆角
  pre {
    border-radius: 6px;
    code { font-size: .9em; }
  }

  // 图片圆角
  img {
    border-radius: 6px;
    max-width: 100%;
  }
}
</style>
