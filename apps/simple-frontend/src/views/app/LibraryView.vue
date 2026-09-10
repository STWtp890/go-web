<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Compass, FilePlus2, Library, Search, X } from '@lucide/vue'
import { markdownApi } from '@/api/markdown'
import { getApiError, isAbortError } from '@/api/client'
import DocumentCard from '@/components/DocumentCard.vue'
import EmptyState from '@/components/EmptyState.vue'
import PaginationControl from '@/components/PaginationControl.vue'
import type { ApiMeta } from '@/types/api'
import type { MarkdownSummary } from '@/types/domain'

type LibraryMode = 'mine' | 'public' | 'search'
interface Props { mode: LibraryMode }
const props = defineProps<Props>()

const pageSize = 9

const route = useRoute()
const router = useRouter()
const documents = ref<MarkdownSummary[]>([])
const meta = ref<ApiMeta>({ page: 1, per_page: 9, total: 0, total_pages: 0 })
const loading = ref(false)
const errorMessage = ref('')
const searchInput = ref('')
const searchInputError = ref('')
let requestController: AbortController | undefined

const activeSearchQuery = computed(() => {
  if (props.mode !== 'search') return ''
  return typeof route.query.q === 'string' ? route.query.q.trim() : ''
})
const activePage = computed(() => {
  if (props.mode !== 'search' || typeof route.query.page !== 'string') return 1
  const page = Number(route.query.page)
  return Number.isInteger(page) && page > 0 ? page : 1
})

const content = computed(() => ({
  mine: { kicker: 'YOUR LIBRARY', title: '我的文稿', description: '所有公开与私密文稿都在这里。', icon: Library },
  public: { kicker: 'COMMUNITY WRITING', title: '公开广场', description: '发现其他创作者公开分享的想法。', icon: Compass },
  search: { kicker: 'FULL-TEXT SEARCH', title: '搜索文稿', description: '仅搜索你自己的标题、摘要与正文。', icon: Search },
}[props.mode]))

function resetResults(page = 1) {
  documents.value = []
  meta.value = { page, per_page: pageSize, total: 0, total_pages: 0 }
}

async function load(page = 1) {
  const keyword = activeSearchQuery.value
  if (props.mode === 'search' && !keyword) {
    requestController?.abort()
    requestController = undefined
    resetResults()
    loading.value = false
    errorMessage.value = ''
    return
  }

  requestController?.abort()
  const controller = new AbortController()
  requestController = controller
  loading.value = true
  errorMessage.value = ''
  documents.value = []
  try {
    const result = props.mode === 'mine'
      ? await markdownApi.mine({ page, pageSize, signal: controller.signal })
      : props.mode === 'public'
        ? await markdownApi.public({ page, pageSize, signal: controller.signal })
        : await markdownApi.search(keyword, { page, pageSize, signal: controller.signal })
    documents.value = result.items
    meta.value = result.meta
  } catch (error) {
    if (isAbortError(error)) return
    resetResults(page)
    errorMessage.value = getApiError(error).message
  } finally {
    if (requestController === controller) {
      requestController = undefined
      loading.value = false
    }
  }
}

async function submitSearch() {
  const keyword = searchInput.value.trim()
  if (!keyword) {
    searchInputError.value = '请输入搜索关键词'
    return
  }
  if ([...keyword].length > 100) {
    searchInputError.value = '搜索关键词不能超过 100 个字符'
    return
  }

  searchInput.value = keyword
  searchInputError.value = ''
  if (keyword === activeSearchQuery.value && activePage.value === 1) {
    await load(1)
    return
  }
  await router.push({ name: 'search', query: { q: keyword } })
}

async function clearSearch() {
  searchInput.value = ''
  searchInputError.value = ''
  await router.replace({ name: 'search' })
}

async function changePage(page: number) {
  if (props.mode !== 'search') {
    await load(page)
    return
  }
  await router.push({
    name: 'search',
    query: { q: activeSearchQuery.value, page: page > 1 ? String(page) : undefined },
  })
}

watch(
  [() => props.mode, () => route.query.q, () => route.query.page],
  () => {
    searchInput.value = activeSearchQuery.value
    searchInputError.value = ''
    void load(activePage.value)
  },
  { immediate: true },
)

onBeforeUnmount(() => requestController?.abort())
</script>

<template>
  <div class="page library-page">
    <header class="page-header">
      <div>
        <span class="page-kicker">{{ content.kicker }}</span>
        <h1><component :is="content.icon" :size="27" />{{ content.title }}</h1>
        <p>{{ content.description }}</p>
      </div>
      <RouterLink v-if="mode === 'mine'" :to="{ name: 'editor' }" class="button button--primary"><FilePlus2 :size="17" />新建文稿</RouterLink>
    </header>

    <form v-if="mode === 'search'" class="search-bar" role="search" aria-label="搜索我的 Markdown 文稿" @submit.prevent="submitSearch">
      <Search :size="20" />
      <input v-model="searchInput" type="search" maxlength="100" autocomplete="off" placeholder="输入关键词，搜索你的文稿…" aria-label="搜索关键词" aria-describedby="search-help" :aria-invalid="Boolean(searchInputError)" @input="searchInputError = ''" />
      <button v-if="searchInput" type="button" class="icon-button" aria-label="清除搜索" @click="clearSearch"><X :size="17" /></button>
      <button type="submit" class="button button--dark" :disabled="loading">{{ loading ? '搜索中…' : '搜索' }}</button>
    </form>
    <p v-if="mode === 'search'" id="search-help" class="search-help" :class="{ 'search-help--error': searchInputError }" :role="searchInputError ? 'alert' : undefined">
      {{ searchInputError || '支持中文与英文关键词，匹配标题、摘要和正文，最多 100 个字符。' }}
    </p>

    <div class="library-toolbar">
      <span>{{ loading ? (mode === 'search' ? '正在搜索…' : '正在加载…') : mode === 'search' && activeSearchQuery ? `“${activeSearchQuery}” 共找到 ${meta.total} 篇文稿` : `共 ${meta.total} 篇文稿` }}</span>
      <span v-if="mode === 'public'">按更新时间展示</span>
      <span v-else-if="mode === 'search' && activeSearchQuery">按相关度排序</span>
    </div>
    <p v-if="errorMessage" class="inline-error" role="alert">{{ errorMessage }} <button type="button" @click="load(meta.page)">重试</button></p>

    <div v-if="loading" class="document-grid"><div v-for="n in 6" :key="n" class="skeleton-card" /></div>
    <div v-else-if="documents.length" class="document-grid"><DocumentCard v-for="document in documents" :key="document.markdownId" :document="document" /></div>
    <EmptyState v-else-if="mode === 'search' && !activeSearchQuery" title="输入一个关键词" description="可以搜索你自己的标题、摘要和正文内容。" />
    <EmptyState v-else :title="mode === 'mine' ? '还没有文稿' : mode === 'public' ? '广场暂时很安静' : `没有找到“${activeSearchQuery}”`" :description="mode === 'mine' ? '写下第一篇内容，开始构建你的文字空间。' : mode === 'public' ? '还没有人公开分享作品。' : '换一个关键词，也许会有新的发现。'">
      <RouterLink v-if="mode === 'mine'" :to="{ name: 'editor' }" class="button button--soft">新建文稿</RouterLink>
    </EmptyState>

    <PaginationControl :page="meta.page" :total-pages="meta.total_pages" :total="meta.total" :disabled="loading" @change="changePage" />
  </div>
</template>
