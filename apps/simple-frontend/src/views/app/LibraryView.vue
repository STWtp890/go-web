<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Compass, FilePlus2, Library, Search, X } from '@lucide/vue'
import { documentApi } from '@/api/document'
import { getApiError, isAbortError } from '@/api/client'
import DocumentCard from '@/components/DocumentCard.vue'
import EmptyState from '@/components/EmptyState.vue'
import PaginationControl from '@/components/PaginationControl.vue'
import { useCursorPager } from '@/composables/useCursorPager'
import type { ApiMeta } from '@/types/api'
import type { DocumentSummary } from '@/types/domain'
import { subscribeSessionChanges } from '@/utils/session-events'

type LibraryMode = 'mine' | 'public' | 'search'
interface Props { mode: LibraryMode }
const props = defineProps<Props>()

const pageSizes = [9, 18, 36]

const route = useRoute()
const router = useRouter()
const {
  pageSize,
  page: pagerPage,
  totalPages: pagerTotalPages,
  total: pagerTotal,
  hasNext,
  hasPrev,
  currentCursor,
  nextCursor,
  record: recordCursor,
  reset: resetPager,
  setPageSize,
  goTo: goToPagerPage,
} = useCursorPager(9)
const documents = ref<DocumentSummary[]>([])
// 检索是页码分页（响应带真实总数），列表是游标分页（由游标栈维护历史）。
const searchMeta = ref<ApiMeta>({ page: 1, per_page: 9, total: 0, total_pages: 0 })
const loading = ref(false)
const errorMessage = ref('')
const searchInput = ref('')
const searchInputError = ref('')
let requestController: AbortController | undefined

const meta = computed<ApiMeta>(() => {
  if (props.mode === 'search') return searchMeta.value
  return {
    page: pagerPage.value,
    per_page: pageSize.value,
    total: pagerTotal.value,
    total_pages: pagerTotalPages.value,
    nextCursor: nextCursor.value,
  }
})

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

function resetResults() {
  documents.value = []
  searchMeta.value = { page: 1, per_page: pageSize.value, total: 0, total_pages: 0 }
}

/**
 * 读取当前页。cursor 为空表示第一页；列表模式下它只能来自服务端响应或游标栈，
 * 绝不由页码换算。
 */
async function load(cursor?: string) {
  const keyword = activeSearchQuery.value
  if (props.mode === 'search' && !keyword) {
    requestController?.abort()
    requestController = undefined
    resetPager()
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
    if (props.mode === 'search') {
      const result = await documentApi.search(keyword, { page: activePage.value, pageSize: pageSize.value, signal: controller.signal })
      documents.value = result.items
      // 总数与总页数来自检索服务给出的“真正可翻页读到”的结果数，不由本页条数推断。
      searchMeta.value = result.meta
      // 结果集可能缩小（旧链接、索引更新）。越界页是读不到内容的空页，不能把它当作
      // 一个结果页展示：回到最后一页重读。
      const lastPage = Math.max(1, result.meta.total_pages)
      if (result.meta.total > 0 && activePage.value > lastPage) {
        await router.replace({ name: 'search', query: { q: keyword, page: lastPage > 1 ? String(lastPage) : undefined } })
        return
      }
    } else {
      const options = { pageSize: pageSize.value, cursor, page: pagerPage.value, signal: controller.signal }
      const result = props.mode === 'mine'
        ? await documentApi.mine(options)
        : await documentApi.public(options)
      documents.value = result.items
      // 把服务端的下一个前向游标与真实总数交给游标栈。
      recordCursor(result.meta.nextCursor, result.meta.total)
    }
  } catch (error) {
    if (isAbortError(error)) return
    if (props.mode === 'search') resetResults()
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
    await load()
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
  if (props.mode === 'search') {
    await router.push({
      name: 'search',
      query: { q: activeSearchQuery.value, page: page > 1 ? String(page) : undefined },
    })
    return
  }
  // 后退回到栈中的上一个游标，前进使用服务端下发的下一个游标。
  const step = goToPagerPage(page)
  if (step) await load(step.cursor)
}

/** 修改每页数量会改变分页边界，原游标不再有效：重置游标与历史后从第一页重读。 */
async function changePageSize(event: Event) {
  const size = Number((event.target as HTMLSelectElement).value)
  if (!Number.isFinite(size) || size <= 0 || size === pageSize.value) return
  setPageSize(size)
  if (props.mode === 'search') {
    if (activePage.value !== 1) {
      await router.replace({ name: 'search', query: { q: activeSearchQuery.value || undefined } })
      return
    }
    await load()
    return
  }
  await load()
}

watch(
  [() => props.mode, () => route.query.q, () => route.query.page],
  ([mode], [previousMode]) => {
    // 切换列表类型必须丢弃上一类的游标历史，否则会把 A 列表的游标用于 B 列表。
    if (mode !== previousMode) resetPager()
    searchInput.value = activeSearchQuery.value
    searchInputError.value = ''
    void load()
  },
  { immediate: true },
)

// 切换账号后游标与已加载内容是上一个账号的，必须整体丢弃。
const unsubscribeSession = subscribeSessionChanges((event) => {
  if (event.scope !== 'user') return
  requestController?.abort()
  requestController = undefined
  resetPager()
  resetResults()
  loading.value = false
})

onBeforeUnmount(() => {
  requestController?.abort()
  unsubscribeSession()
})
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
      <label class="library-toolbar__size">
        每页
        <select :value="pageSize" :disabled="loading" aria-label="每页数量" @change="changePageSize">
          <option v-for="size in pageSizes" :key="size" :value="size">{{ size }} 篇</option>
        </select>
      </label>
    </div>
    <p v-if="errorMessage" class="inline-error" role="alert">{{ errorMessage }} <button type="button" @click="load(props.mode === 'search' ? undefined : currentCursor)">重试</button></p>
    <p v-if="mode === 'search' && !loading && documents.length > 0 && meta.truncated" class="search-notice" role="status">
      结果未完整展示：本页之后还有匹配结果，共 {{ meta.total }} 条，可继续翻页查看。
    </p>

    <div v-if="loading" class="document-grid"><div v-for="n in 6" :key="n" class="skeleton-card" /></div>
    <div v-else-if="documents.length" class="document-grid"><DocumentCard v-for="document in documents" :key="document.documentId" :document="document" /></div>
    <EmptyState v-else-if="mode === 'search' && !activeSearchQuery" title="输入一个关键词" description="可以搜索你自己的标题、摘要和正文内容。" />
    <EmptyState v-else :title="mode === 'mine' ? '还没有文稿' : mode === 'public' ? '广场暂时很安静' : `没有找到“${activeSearchQuery}”`" :description="mode === 'mine' ? '写下第一篇内容，开始构建你的文字空间。' : mode === 'public' ? '还没有人公开分享作品。' : '换一个关键词，也许会有新的发现。'">
      <RouterLink v-if="mode === 'mine'" :to="{ name: 'editor' }" class="button button--soft">新建文稿</RouterLink>
    </EmptyState>

    <PaginationControl
      :page="mode === 'search' ? meta.page : pagerPage"
      :total-pages="meta.total_pages"
      :total="meta.total"
      :has-prev="mode === 'search' ? activePage > 1 : hasPrev"
      :has-next="mode === 'search' ? activePage < meta.total_pages : hasNext"
      :disabled="loading"
      @change="changePage"
    />
  </div>
</template>

<style scoped>
.library-toolbar__size {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  margin-left: auto;
}

.library-toolbar__size select {
  padding: 0.15rem 0.35rem;
  border-radius: 6px;
  border: 1px solid var(--border-color, rgba(148, 163, 184, 0.4));
  background: transparent;
  color: inherit;
  font: inherit;
}

.search-notice {
  margin: 0.25rem 0 0.75rem;
  padding: 0.5rem 0.75rem;
  border-radius: 8px;
  border: 1px solid rgba(234, 179, 8, 0.45);
  background: rgba(234, 179, 8, 0.1);
  color: inherit;
  font-size: 0.9rem;
}
</style>
