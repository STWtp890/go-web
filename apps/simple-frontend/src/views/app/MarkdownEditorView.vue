<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Check, ChevronLeft, Eye, FileText, Globe2, LockKeyhole, Save } from '@lucide/vue'
import { documentApi } from '@/api/document'
import { getApiError } from '@/api/client'
import MarkdownBody from '@/components/MarkdownBody.vue'
import { useToastStore } from '@/stores/toast'
import type { Visibility } from '@/types/domain'

const DRAFT_KEY = 'paperplane:markdown-draft'
const route = useRoute()
const router = useRouter()
const toast = useToastStore()
const title = ref('')
const content = ref('')
const visibility = ref<Visibility>('private')
const loading = ref(false)
const loadingDetail = ref(false)
const errorMessage = ref('')
const draftRestored = ref(false)
const mobilePreview = ref(false)
// 打开详情时拿到的聚合修订号：保存时回传它，保存即乐观并发更新。
const revision = ref<number | undefined>(undefined)
// 同一次保存的重试复用同一个幂等键，后端据此重放而不是追加第二个版本。
const pendingRequestID = ref('')

const documentId = computed(() => (typeof route.params.documentId === 'string' ? route.params.documentId : ''))
const isEditing = computed(() => Boolean(documentId.value))
const characterCount = computed(() => content.value.length)
const canSubmit = computed(() => title.value.trim().length > 0 && content.value.trim().length > 0 && !loading.value && !loadingDetail.value)

function newRequestID(): string {
  const generated = globalThis.crypto?.randomUUID?.()
  return generated ?? `save-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

onMounted(async () => {
  if (isEditing.value) {
    loadingDetail.value = true
    try {
      const document = await documentApi.detail(documentId.value)
      title.value = document.title
      content.value = document.content
      visibility.value = document.visibility
      revision.value = document.revision
    } catch (error) {
      errorMessage.value = getApiError(error).message
    } finally {
      loadingDetail.value = false
    }
    return
  }
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
  // 编辑既有文稿时不落本地草稿：它属于“新建”，恢复它会覆盖正在编辑的正文。
  if (isEditing.value) return
  if (title.value || content.value) localStorage.setItem(DRAFT_KEY, JSON.stringify({ title: title.value, content: content.value, visibility: visibility.value }))
}, { flush: 'post' })

function discardDraft() {
  title.value = ''
  content.value = ''
  visibility.value = 'private'
  localStorage.removeItem(DRAFT_KEY)
  draftRestored.value = false
}

async function save() {
  if (!canSubmit.value) return
  loading.value = true
  errorMessage.value = ''
  if (!pendingRequestID.value) pendingRequestID.value = newRequestID()
  try {
    if (isEditing.value) {
      const updated = await documentApi.update(documentId.value, {
        title: title.value.trim(),
        content: content.value,
        visibility: visibility.value,
        expectedRevision: revision.value,
        requestId: pendingRequestID.value,
      })
      pendingRequestID.value = ''
      // 重放返回的是当前已提交状态（可能已经再次被改动），因此以服务端返回为准，
      // 而不是假定这次输入的正文已经写入。
      revision.value = updated.revision
      if (updated.replayed) {
        // 本次请求此前已经生效过：不能说“刚刚保存成功”，否则重复提交会被误认为
        // 产生了新的版本。
        toast.show({
          tone: 'info',
          title: '本次保存此前已生效',
          message: `该请求已由版本 ${updated.appliedVersionId || '（未知）'} 生效，本次没有写入新版本；如需保存新的修改，请重新保存。`,
        })
      } else {
        toast.show({ tone: 'success', title: '文稿已保存', message: visibility.value === 'public' ? '这篇文稿现在是公开的' : '这篇文稿仅你自己可读' })
      }
      await router.replace({ name: 'document-detail', params: { documentId: documentId.value } })
      return
    }
    const created = await documentApi.create({ title: title.value.trim(), content: content.value, visibility: visibility.value, requestId: pendingRequestID.value })
    pendingRequestID.value = ''
    localStorage.removeItem(DRAFT_KEY)
    toast.show({ tone: 'success', title: '文稿已保存', message: visibility.value === 'public' ? '现在可以在公开广场看到它' : '仅你自己可以阅读这篇文稿' })
    await router.replace({ name: 'document-detail', params: { documentId: created.documentId } })
  } catch (error) {
    // 保留 pendingRequestID：用户重试同一次保存时后端会识别为重放，不会写入第二个版本。
    errorMessage.value = getApiError(error).message
  } finally { loading.value = false }
}
</script>

<template>
  <div class="editor-page">
    <header class="editor-toolbar">
      <button type="button" class="button button--ghost button--small" @click="router.back()"><ChevronLeft :size="17" />返回</button>
      <div class="editor-toolbar__status"><Save :size="15" /><span>{{ isEditing ? '保存后会立即生效' : '草稿自动保存在本机' }}</span></div>
      <button type="button" class="button button--soft button--small editor-preview-toggle" @click="mobilePreview = !mobilePreview"><Eye :size="16" />{{ mobilePreview ? '继续编辑' : '预览' }}</button>
      <button type="button" class="button button--primary" :disabled="!canSubmit" @click="save"><Check :size="17" />{{ loading ? '正在保存…' : isEditing ? '保存修改' : '保存文稿' }}</button>
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
