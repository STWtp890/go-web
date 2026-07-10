<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-key text-primary mr-2" />权限管理</h3>
        <button class="btn btn-primary btn-sm" @click="openCreate"><i class="icon-plus" /> 新建权限</button>
      </div>
      <div class="card"><div class="card-body">
        <div v-if="errorMsg" class="alert alert-danger mx-3 mt-3">{{ errorMsg }}</div>
        <div v-if="loading" class="text-center py-4"><div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载中...</p></div>
        <template v-else>
          <div><table class="table table-striped"><thead><tr><th>权限标识</th><th>资源</th><th>操作</th><th>标签</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-if="list.length===0"><td colspan="5" class="text-center text-muted">暂无权限</td></tr>
              <tr v-for="item in list" :key="item.permissionId">
                <td><code>{{ item.permissionId }}</code></td><td>{{ item.resource }}</td>
                <td><span class="badge badge-success">{{ item.action }}</span></td><td>{{ item.label||'-' }}</td>
                <td><button class="btn btn-outline-primary btn-sm" @click="openEdit(item)">编辑</button><button class="btn btn-outline-danger btn-sm ml-1" @click="del(item)">删除</button></td>
              </tr>
            </tbody></table></div>
          <div v-if="totalPages>1" class="d-flex justify-content-center mt-3">
            <button class="btn btn-sm btn-default" :disabled="page<=1" @click="page--">上一页</button>
            <span class="mx-2 align-self-center text-muted">第 {{ page }}/{{ totalPages }} 页</span>
            <button class="btn btn-sm btn-default" :disabled="page>=totalPages" @click="page++">下一页</button>
          </div>
        </template>
      </div></div>
      <modal :show="showModal" @update:show="closeModal">
        <template #header><h5 class="modal-title">{{ editing ? '编辑权限' : '新建权限' }}</h5></template>
        <div>
          <div v-if="modalError" class="alert alert-danger">{{ modalError }}</div>
          <div class="form-group"><label class="form-control-label">权限标识</label><input v-model="form.permissionId" type="number" class="form-control" placeholder="如：1" :disabled="!!editing" required /></div>
          <div class="form-group"><label class="form-control-label">资源名</label><input v-model="form.resource" class="form-control" placeholder="如：announcement" required /></div>
          <div class="form-group"><label class="form-control-label">操作类型</label><select v-model="form.action" class="form-control"><option value="">-- 请选择 --</option><option value="create">create</option><option value="read">read</option><option value="update">update</option><option value="delete">delete</option><option value="list">list</option><option value="assign">assign</option></select></div>
          <div class="form-group"><label class="form-control-label">标签描述</label><input v-model="form.label" class="form-control" placeholder="如：创建公告" /></div>
        </div>
        <template #footer>
          <button class="btn btn-secondary" @click="closeModal">取消</button>
          <button class="btn btn-primary" :disabled="modalLoading" @click="modalSubmit">{{ modalLoading?'保存中...':'保存' }}</button>
        </template>
      </modal>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { permissionAPI } from '@/api/rbac'
import { Modal } from '@/components'
import { usePagination } from '@/composables/usePagination'
import { useAsync } from '@/composables/useAsync'
import { useModal } from '@/composables/useModal'
import { notify } from '@/composables/useNotify'
import { userMsg } from '@/utils/error'

// ── 列表状态 ──
const list = ref([])
const { loading, errorMsg, run } = useAsync()
async function fetchData(p) {
  const pg = p ?? page.value
  await run(async () => {
    const r = await permissionAPI.list({ page: pg, pageSize: pageSize.value })
    list.value = r.permissionList?.permissionList || []
    totalCount.value = r.totalCount || 0
  }, '加载权限失败')
}

const { page, pageSize, totalCount, totalPages } = usePagination(fetchData)

// ── 删除 ──
async function del(item) {
  const ok = await notify.confirmDelete(`确定删除权限「${item.permissionId}」吗？`)
  if (!ok) return
  try {
    await permissionAPI.delete(item.permissionId)
    await fetchData()
  } catch (e) {
    notify.error(userMsg(e, '删除'))
  }
}

// ── Modal 状态 ──
const { showModal, editing, modalLoading, modalError, openModal, closeModal } = useModal()
const form = ref({ permissionId: '', resource: '', action: '', label: '' })

function openCreate() {
  form.value = { permissionId: '', resource: '', action: '', label: '' }
  openModal()
}

function openEdit(item) {
  form.value = {
    permissionId: item.permissionId || '',
    resource: item.resource || '',
    action: item.action || '',
    label: item.label || ''
  }
  openModal(item)
}

async function modalSubmit() {
  modalError.value = ''
  modalLoading.value = true

  if (!editing.value && !form.value.permissionId) {
    modalError.value = '请输入权限标识'
    modalLoading.value = false
    return
  }

  try {
    const payload = { ...form.value, permissionId: Number(form.value.permissionId) }
    if (editing.value) {
      await permissionAPI.update(editing.value.permissionId, payload)
    } else {
      await permissionAPI.create(payload)
    }
    closeModal()
    await fetchData()
  } catch (e) {
    modalError.value = (e.response && e.response.data && e.response.data.message) || '操作失败'
  } finally {
    modalLoading.value = false
  }
}

// ── 初始加载 ──
fetchData().catch(() => {})  // 错误已由 errorMsg 展示
</script>
