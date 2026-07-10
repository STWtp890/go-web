<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-pencil text-primary mr-2" />{{ isEdit ? '编辑文章' : '发布文章' }}</h3>
      </div>
      <div class="card"><div class="card-body">
        <form @submit.prevent="submit">
          <div class="form-group"><label class="form-control-label">文章标题</label><input v-model="form.title" class="form-control" placeholder="请输入文章标题" required /></div>
          <div class="form-group"><label class="form-control-label">Markdown 内容 <small class="text-muted ml-2">支持完整 Markdown 语法</small></label><textarea v-model="form.content" class="form-control" placeholder="# 文章标题&#10;&#10;开始撰写您的文章..." required></textarea></div>
          <div class="d-flex justify-content-between">
            <router-link to="/articles" class="btn btn-outline-secondary">取消</router-link>
            <button class="btn btn-primary" :disabled="loading" type="submit">{{ loading ? '保存中...' : isEdit ? '更新文章' : '发布文章' }}</button>
          </div>
        </form>
      </div></div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { markdownAPI } from '@/api/markdown'
import { notify } from '@/composables/useNotify'
import { floatAlert } from '@/composables/useFloatAlert'
import { userMsg } from '@/utils/error'

const route = useRoute(); const router = useRouter()
const isEdit = computed(() => !!route.params.id)
const loading = ref(false)
const form = reactive({ title: '', content: '' })

onMounted(async () => {
  const id = route.params.id
  if (id) {
    try { const r = await markdownAPI.preview(id); form.title = r.markdown?.title || ''; form.content = r.markdown?.content || '' }
    catch (e) { floatAlert.warning('加载文章失败') }
  }
})

async function submit() {
  loading.value = true
  const payload = { title: form.title, content: form.content }
  try {
    if (isEdit.value) {
      await markdownAPI.update(route.params.id, payload)
      notify.warning('内容已保存，需重新审核')
    } else {
      await markdownAPI.upload(payload)
      notify.success('文章已发布，等待审核')
    }
    router.push('/articles')
  } catch (e) {
    floatAlert.warning(userMsg(e, '保存'))
  } finally { loading.value = false }
}
</script>

<style scoped>
.card {
  max-height: calc(100vh - 180px);
  display: flex;
  flex-direction: column;
}
.card > .card-body {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.card-body > form {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
/* 标题行：固定高度 */
.form-group:first-of-type {
  flex-shrink: 0;
}
/* 内容区：撑满剩余空间 */
.form-group:nth-of-type(2) {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.form-group:nth-of-type(2) textarea {
  flex: 1;
  min-height: 200px;
  resize: none;
}
/* 按钮行：固定高度 */
.d-flex.justify-content-between {
  flex-shrink: 0;
}
</style>
