<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Compass, FilePlus2, Library, Search, X } from '@lucide/vue'
import { markdownApi } from '@/api/markdown'
import { getApiError } from '@/api/client'
import DocumentCard from '@/components/DocumentCard.vue'
import EmptyState from '@/components/EmptyState.vue'
import PaginationControl from '@/components/PaginationControl.vue'
import type { ApiMeta } from '@/types/api'
import type { MarkdownSummary } from '@/types/domain'

type LibraryMode = 'mine' | 'public' | 'search'
interface Props { mode: LibraryMode }
const props = defineProps<Props>()

const route = useRoute()
const router = useRouter()
const documents = ref<MarkdownSummary[]>([])
const meta = ref<ApiMeta>({ page: 1, per_page: 9, total: 0, total_pages: 0 })
const loading = ref(false)
const errorMessage = ref('')
const searchQuery = ref(typeof route.query.q === 'string' ? route.query.q : '')

const content = computed(() => ({
  mine: { kicker: 'YOUR LIBRARY', title: '我的文稿', description: '所有公开与私密文稿都在这里。', icon: Library },
  public: { kicker: 'COMMUNITY WRITING', title: '公开广场', description: '发现其他创作者公开分享的想法。', icon: Compass },
  search: { kicker: 'FULL-TEXT SEARCH', title: '搜索文稿', description: '仅搜索你自己的标题、摘要与正文。', icon: Search },
}[props.mode]))

async function load(page = 1) {
  if (props.mode === 'search' && !searchQuery.value.trim()) {
    documents.value = []
    meta.value = { page: 1, per_page: 9, total: 0, total_pages: 0 }
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    const result = props.mode === 'mine'
      ? await markdownApi.mine(page)
      : props.mode === 'public'
        ? await markdownApi.public(page)
        : await markdownApi.search(searchQuery.value.trim(), page)
    documents.value = result.items
    meta.value = result.meta
  } catch (error) {
    errorMessage.value = getApiError(error).message
  } finally {
    loading.value = false
  }
}

async function submitSearch() {
  if (!searchQuery.value.trim()) return
  await router.replace({ name: 'search', query: { q: searchQuery.value.trim() } })
  await load(1)
}

function clearSearch() {
  searchQuery.value = ''
  documents.value = []
  void router.replace({ name: 'search' })
}

watch(() => props.mode, () => {
  searchQuery.value = typeof route.query.q === 'string' ? route.query.q : ''
  void load(1)
})
onMounted(() => load())
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

    <form v-if="mode === 'search'" class="search-bar" @submit.prevent="submitSearch">
      <Search :size="20" />
      <input v-model="searchQuery" type="search" placeholder="输入关键词，搜索你的文稿…" aria-label="搜索关键词" />
      <button v-if="searchQuery" type="button" class="icon-button" aria-label="清除搜索" @click="clearSearch"><X :size="17" /></button>
      <button type="submit" class="button button--dark">搜索</button>
    </form>

    <div class="library-toolbar"><span>{{ loading ? '正在加载…' : `共 ${meta.total} 篇文稿` }}</span><span v-if="mode === 'public'">按更新时间展示</span></div>
    <p v-if="errorMessage" class="inline-error" role="alert">{{ errorMessage }} <button type="button" @click="load(meta.page)">重试</button></p>

    <div v-if="loading" class="document-grid"><div v-for="n in 6" :key="n" class="skeleton-card" /></div>
    <div v-else-if="documents.length" class="document-grid"><DocumentCard v-for="document in documents" :key="document.markdownId" :document="document" /></div>
    <EmptyState v-else-if="mode === 'search' && !searchQuery" title="输入一个关键词" description="可以搜索标题、摘要和正文内容。" />
    <EmptyState v-else :title="mode === 'mine' ? '还没有文稿' : mode === 'public' ? '广场暂时很安静' : '没有找到相关文稿'" :description="mode === 'mine' ? '写下第一篇内容，开始构建你的文字空间。' : mode === 'public' ? '还没有人公开分享作品。' : '换一个关键词，也许会有新的发现。'">
      <RouterLink v-if="mode === 'mine'" :to="{ name: 'editor' }" class="button button--soft">新建文稿</RouterLink>
    </EmptyState>

    <PaginationControl :page="meta.page" :total-pages="meta.total_pages" :total="meta.total" @change="load" />
  </div>
</template>
