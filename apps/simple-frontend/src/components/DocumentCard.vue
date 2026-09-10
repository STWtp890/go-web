<script setup lang="ts">
import { ArrowUpRight, Clock3, Globe2, LockKeyhole } from '@lucide/vue'
import type { MarkdownSummary } from '@/types/domain'
import { formatRelativeDate } from '@/utils/format'

interface Props { document: MarkdownSummary }
defineProps<Props>()
</script>

<template>
  <RouterLink :to="{ name: 'markdown-detail', params: { markdownId: document.markdownId } }" class="document-card">
    <div class="document-card__top">
      <span class="visibility-pill" :class="`visibility-pill--${document.visibility}`">
        <Globe2 v-if="document.visibility === 'public'" :size="13" />
        <LockKeyhole v-else :size="13" />
        {{ document.visibility === 'public' ? '公开' : '私密' }}
      </span>
      <ArrowUpRight class="document-card__arrow" :size="19" />
    </div>
    <div>
      <h3>{{ document.title }}</h3>
      <p>{{ document.summary || '这篇文稿还没有摘要。' }}</p>
    </div>
    <footer>
      <span><Clock3 :size="14" />{{ formatRelativeDate(document.updatedAt) }}</span>
      <span v-if="document.authorId">作者 #{{ document.authorId }}</span>
    </footer>
  </RouterLink>
</template>
