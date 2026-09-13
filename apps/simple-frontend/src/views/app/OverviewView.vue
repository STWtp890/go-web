<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ArrowRight, BookOpen, Compass, FilePlus2, PenLine, Search } from '@lucide/vue'
import { documentApi } from '@/api/document'
import DocumentCard from '@/components/DocumentCard.vue'
import type { DocumentSummary } from '@/types/domain'
import { useUserSessionStore } from '@/stores/session'

const session = useUserSessionStore()
const loading = ref(true)
const mineTotal = ref(0)
const publicTotal = ref(0)
const recentDocuments = ref<DocumentSummary[]>([])

onMounted(async () => {
  const [mine, publicList] = await Promise.allSettled([
    documentApi.mine({ pageSize: 3 }),
    documentApi.public({ pageSize: 1 }),
  ])
  if (mine.status === 'fulfilled') {
    mineTotal.value = mine.value.meta.total
    recentDocuments.value = mine.value.items
  }
  if (publicList.status === 'fulfilled') publicTotal.value = publicList.value.meta.total
  loading.value = false
})
</script>

<template>
  <div class="page overview-page">
    <header class="page-header page-header--hero">
      <div>
        <span class="page-kicker">YOUR WORKSPACE</span>
        <h1>{{ session.subject ? `你好，用户 #${session.subject}` : '你好，欢迎回来' }}</h1>
        <p>今天也留一点时间，整理脑海里尚未成形的想法。</p>
      </div>
      <RouterLink :to="{ name: 'editor' }" class="button button--primary button--large"><PenLine :size="18" />开始写作</RouterLink>
    </header>

    <section class="stats-grid" :aria-busy="loading">
      <RouterLink :to="{ name: 'mine' }"><span class="stat-icon stat-icon--terracotta"><BookOpen :size="20" /></span><div><strong>{{ loading ? '—' : mineTotal }}</strong><span>我的文稿</span></div><ArrowRight :size="18" /></RouterLink>
      <RouterLink :to="{ name: 'explore' }"><span class="stat-icon stat-icon--green"><Compass :size="20" /></span><div><strong>{{ loading ? '—' : publicTotal }}</strong><span>公开作品</span></div><ArrowRight :size="18" /></RouterLink>
      <RouterLink :to="{ name: 'search' }"><span class="stat-icon stat-icon--blue"><Search :size="20" /></span><div><strong>全文</strong><span>搜索文稿</span></div><ArrowRight :size="18" /></RouterLink>
      <RouterLink :to="{ name: 'editor' }"><span class="stat-icon stat-icon--gold"><FilePlus2 :size="20" /></span><div><strong>新建</strong><span>开始写作</span></div><ArrowRight :size="18" /></RouterLink>
    </section>

    <section class="overview-section">
      <div class="section-row"><div><span class="page-kicker">RECENT NOTES</span><h2>最近文稿</h2></div><RouterLink :to="{ name: 'mine' }">查看全部<ArrowRight :size="16" /></RouterLink></div>
      <div v-if="loading" class="document-grid"><div v-for="n in 3" :key="n" class="skeleton-card" /></div>
      <div v-else-if="recentDocuments.length" class="document-grid"><DocumentCard v-for="document in recentDocuments" :key="document.documentId" :document="document" /></div>
      <div v-else class="first-note">
        <span><FilePlus2 :size="25" /></span>
        <div><h3>你的第一篇文稿，从这里开始</h3><p>不必完整，先写下一句话就好。</p></div>
        <RouterLink :to="{ name: 'editor' }" class="button button--soft">新建文稿</RouterLink>
      </div>
    </section>

    <section class="overview-quote"><span>“</span><blockquote>写作，是把模糊的感受变成可以被重新看见的形状。</blockquote><i /></section>
  </div>
</template>
