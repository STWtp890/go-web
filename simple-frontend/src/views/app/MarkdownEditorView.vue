<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Check, ChevronLeft, Eye, FileText, Globe2, LockKeyhole, Save } from '@lucide/vue'
import { markdownApi } from '@/api/markdown'
import { getApiError } from '@/api/client'
import MarkdownBody from '@/components/MarkdownBody.vue'
import { useToastStore } from '@/stores/toast'
import type { Visibility } from '@/types/domain'

const DRAFT_KEY = 'paperplane:markdown-draft'
const router = useRouter()
const toast = useToastStore()
const title = ref('')
const content = ref('')
const visibility = ref<Visibility>('private')
const loading = ref(false)
const errorMessage = ref('')
const draftRestored = ref(false)
const mobilePreview = ref(false)

const characterCount = computed(() => content.value.length)
const canSubmit = computed(() => title.value.trim().length > 0 && content.value.trim().length > 0 && !loading.value)

onMounted(() => {
  const raw = localStorage.getItem(DRAFT_KEY)
  if (!raw) return
  try {
    const draft = JSON.parse(raw) as { title?: string; content?: string; visibility?: Visibility }
    title.value = draft.title ?? ''
    content.value = draft.content ?? ''
    visibility.value = draft.visibility ?? 'private'
    draftRestored.value = Boolean(title.value || content.value)
  } catch { localStorage.removeItem(DRAFT_KEY) }
})

watch([title, content, visibility], () => {
  if (title.value || content.value) localStorage.setItem(DRAFT_KEY, JSON.stringify({ title: title.value, content: content.value, visibility: visibility.value }))
}, { flush: 'post' })

function discardDraft() {
  title.value = ''
  content.value = ''
  visibility.value = 'private'
  localStorage.removeItem(DRAFT_KEY)
  draftRestored.value = false
}

async function publish() {
  if (!canSubmit.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    const created = await markdownApi.create({ title: title.value.trim(), content: content.value, visibility: visibility.value })
    localStorage.removeItem(DRAFT_KEY)
    toast.show({ tone: 'success', title: '文稿已保存', message: visibility.value === 'public' ? '现在可以在公开广场看到它' : '仅你自己可以阅读这篇文稿' })
    await router.replace({ name: 'markdown-detail', params: { markdownId: created.markdownId } })
  } catch (error) {
    errorMessage.value = getApiError(error).message
  } finally { loading.value = false }
}
</script>

<template>
  <div class="editor-page">
    <header class="editor-toolbar">
      <button type="button" class="button button--ghost button--small" @click="router.back()"><ChevronLeft :size="17" />返回</button>
      <div class="editor-toolbar__status"><Save :size="15" /><span>草稿自动保存在本机</span></div>
      <button type="button" class="button button--soft button--small editor-preview-toggle" @click="mobilePreview = !mobilePreview"><Eye :size="16" />{{ mobilePreview ? '继续编辑' : '预览' }}</button>
      <button type="button" class="button button--primary" :disabled="!canSubmit" @click="publish"><Check :size="17" />{{ loading ? '正在保存…' : '保存文稿' }}</button>
    </header>

    <p v-if="draftRestored" class="draft-banner">已恢复上次未保存的本地草稿。<button type="button" @click="discardDraft">丢弃草稿</button></p>
    <p v-if="errorMessage" class="inline-error editor-error" role="alert">{{ errorMessage }}</p>

    <div class="editor-workspace" :class="{ 'editor-workspace--preview': mobilePreview }">
      <section class="editor-pane editor-pane--write">
        <div class="editor-meta">
          <label class="editor-title"><span>标题</span><input v-model="title" maxlength="255" placeholder="给这篇文稿一个名字" autofocus /></label>
          <fieldset class="visibility-choice">
            <legend>可见性</legend>
            <label><input v-model="visibility" type="radio" value="private" /><span><LockKeyhole :size="15" />私密</span></label>
            <label><input v-model="visibility" type="radio" value="public" /><span><Globe2 :size="15" />公开</span></label>
          </fieldset>
        </div>
        <div class="editor-textarea">
          <span class="editor-textarea__label"><FileText :size="15" />MARKDOWN</span>
          <textarea v-model="content" placeholder="# 从这里开始&#10;&#10;写下你的想法，支持 **Markdown** 语法。" spellcheck="true" />
          <span class="editor-textarea__count">{{ characterCount.toLocaleString() }} 字符</span>
        </div>
      </section>

      <section class="editor-pane editor-pane--preview">
        <span class="editor-preview__label"><Eye :size="15" />实时预览</span>
        <div class="editor-preview__paper">
          <MarkdownBody v-if="content" :content="content" />
          <div v-else class="preview-placeholder"><FileText :size="32" /><span>预览会在这里出现</span></div>
        </div>
      </section>
    </div>
  </div>
</template>
