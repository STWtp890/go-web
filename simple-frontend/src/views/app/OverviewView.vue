<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ArrowRight, BookOpen, Compass, FilePlus2, MessageCircle, PenLine, Users } from '@lucide/vue'
import { markdownApi } from '@/api/markdown'
import { chatApi } from '@/api/chat'
import DocumentCard from '@/components/DocumentCard.vue'
import type { MarkdownSummary } from '@/types/domain'
import { useUserSessionStore } from '@/stores/session'

const session = useUserSessionStore()
const loading = ref(true)
const mineTotal = ref(0)
const publicTotal = ref(0)
const groupTotal = ref(0)
const recentDocuments = ref<MarkdownSummary[]>([])

onMounted(async () => {
  const [mine, publicList, groups] = await Promise.allSettled([
    markdownApi.mine(1, 3), markdownApi.public(1, 1), chatApi.myGroups(),
  ])
  if (mine.status === 'fulfilled') {
    mineTotal.value = mine.value.meta.total
    recentDocuments.value = mine.value.items
  }
  if (publicList.status === 'fulfilled') publicTotal.value = publicList.value.meta.total
  if (groups.status === 'fulfilled') groupTotal.value = groups.value.length
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
      <article><span class="stat-icon stat-icon--terracotta"><BookOpen :size="20" /></span><div><strong>{{ loading ? '—' : mineTotal }}</strong><span>我的文稿</span></div></article>
      <article><span class="stat-icon stat-icon--green"><Compass :size="20" /></span><div><strong>{{ loading ? '—' : publicTotal }}</strong><span>公开作品</span></div></article>
      <article><span class="stat-icon stat-icon--blue"><Users :size="20" /></span><div><strong>{{ loading ? '—' : groupTotal }}</strong><span>已加入群组</span></div></article>
      <RouterLink :to="{ name: 'chat' }"><span class="stat-icon stat-icon--gold"><MessageCircle :size="20" /></span><div><strong>HTTP</strong><span>消息通道</span></div><ArrowRight :size="18" /></RouterLink>
    </section>

    <section class="overview-section">
      <div class="section-row"><div><span class="page-kicker">RECENT NOTES</span><h2>最近文稿</h2></div><RouterLink :to="{ name: 'mine' }">查看全部<ArrowRight :size="16" /></RouterLink></div>
      <div v-if="loading" class="document-grid"><div v-for="n in 3" :key="n" class="skeleton-card" /></div>
      <div v-else-if="recentDocuments.length" class="document-grid"><DocumentCard v-for="document in recentDocuments" :key="document.markdownId" :document="document" /></div>
      <div v-else class="first-note">
        <span><FilePlus2 :size="25" /></span>
        <div><h3>你的第一篇文稿，从这里开始</h3><p>不必完整，先写下一句话就好。</p></div>
        <RouterLink :to="{ name: 'editor' }" class="button button--soft">新建文稿</RouterLink>
      </div>
    </section>

    <section class="overview-quote"><span>“</span><blockquote>写作，是把模糊的感受变成可以被重新看见的形状。</blockquote><i /></section>
  </div>
</template>
