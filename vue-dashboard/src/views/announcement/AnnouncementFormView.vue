<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-pencil text-primary mr-2" />{{ isEdit ? '编辑公告' : '新建公告' }}</h3>
      </div>
      <div class="card"><div class="card-body">
        <!-- 编辑模式加载原内容 -->
        <div v-if="pageLoading" class="text-center py-5">
          <div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载公告内容...</p>
        </div>
        <template v-else>
        <div v-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
        <form @submit.prevent="submit">
          <div class="form-group"><label class="form-control-label">标题</label><input v-model="form.title" class="form-control" placeholder="请输入公告标题" required /></div>
          <div class="form-group"><label class="form-control-label">内容（支持 Markdown）</label><textarea v-model="form.content" class="form-control" rows="10" placeholder="请输入公告内容..." required></textarea></div>
          <div class="d-flex justify-content-between">
            <router-link to="/notices" class="btn btn-outline-secondary">取消</router-link>
            <button class="btn btn-primary" :disabled="loading" type="submit">{{ loading ? '保存中...' : '保 存' }}</button>
          </div>
        </form>
        </template>
      </div></div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { announcementAPI } from '@/api/announcement'

const route = useRoute()
const router = useRouter()
const isEdit = computed(() => !!route.params.id)
const loading = ref(false)
const pageLoading = ref(false)
const errorMsg = ref('')
const form = reactive({ title: '', content: '' })

// 编辑模式：获取原公告内容填充表单
onMounted(async () => {
  if (!isEdit.value) return
  pageLoading.value = true
  try {
    const r = await announcementAPI.preview(route.params.id)
    const item = r?.announcement
    if (item) {
      form.title = item.title || ''
      form.content = item.content || ''
    }
  } catch (e) {
    errorMsg.value = '加载公告失败：' + ((e.response && e.response.data && e.response.data.message) || e.message || '未知错误')
  } finally {
    pageLoading.value = false
  }
})

async function submit() {
  errorMsg.value = ''; loading.value = true
  try { isEdit.value ? await announcementAPI.update(route.params.id, { ...form }) : await announcementAPI.create({ ...form }); router.push('/notices') }
  catch (e) { errorMsg.value = (e.response && e.response.data && e.response.data.message) || '操作失败' }
  finally { loading.value = false }
}
</script>
