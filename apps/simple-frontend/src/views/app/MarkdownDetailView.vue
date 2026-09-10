<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, CalendarDays, Copy, Globe2, LockKeyhole } from '@lucide/vue'
import { markdownApi } from '@/api/markdown'
import { getApiError } from '@/api/client'
import MarkdownBody from '@/components/MarkdownBody.vue'
import { useToastStore } from '@/stores/toast'
import type { MarkdownDetail } from '@/types/domain'
import { formatDate } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const toast = useToastStore()
const document = ref<MarkdownDetail | null>(null)
const loading = ref(true)
const errorMessage = ref('')
const markdownId = computed(() => String(route.params.markdownId))

onMounted(async () => {
  try { document.value = await markdownApi.detail(markdownId.value) }
  catch (error) { errorMessage.value = getApiError(error).message }
  finally { loading.value = false }
})

async function copyLink() {
  await navigator.clipboard.writeText(window.location.href)
  toast.show({ tone: 'success', title: '链接已复制' })
}
</script>

<template>
  <div class="page detail-page">
    <div v-if="loading" class="detail-skeleton"><div /><div /><div /><div /></div>
    <div v-else-if="errorMessage" class="detail-error"><span>无法打开这篇文稿</span><h1>{{ errorMessage }}</h1><button type="button" class="button button--soft" @click="router.back()"><ArrowLeft :size="17" />返回上一页</button></div>
    <template v-else-if="document">
      <nav class="detail-actions">
        <button type="button" class="button button--ghost button--small" @click="router.back()"><ArrowLeft :size="17" />返回</button>
        <button type="button" class="button button--soft button--small" @click="copyLink"><Copy :size="15" />复制链接</button>
      </nav>
      <article class="detail-paper">
        <header class="detail-paper__header">
          <span class="visibility-pill" :class="`visibility-pill--${document.visibility}`">
            <Globe2 v-if="document.visibility === 'public'" :size="13" /><LockKeyhole v-else :size="13" />{{ document.visibility === 'public' ? '公开文稿' : '私密文稿' }}
          </span>
          <h1>{{ document.title }}</h1>
          <p v-if="document.summary" class="detail-paper__summary">{{ document.summary }}</p>
          <div class="detail-paper__meta"><span><CalendarDays :size="15" />创建于 {{ formatDate(document.createdAt) }}</span><span>更新于 {{ formatDate(document.updatedAt) }}</span></div>
        </header>
        <MarkdownBody :content="document.content" />
      </article>
    </template>
  </div>
</template>
