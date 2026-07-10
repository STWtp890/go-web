<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-tag text-primary mr-2" />角色管理</h3>
        <button class="btn btn-primary btn-sm" @click="openCreate"><i class="icon-plus" /> 新建角色</button>
      </div>
      <div class="card"><div class="card-body">
        <div v-if="errorMsg" class="alert alert-danger mx-3 mt-3">{{ errorMsg }}</div>
        <div v-if="loading" class="text-center py-4"><div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载中...</p></div>
        <template v-else>
          <div><table class="table table-striped"><thead><tr><th>角色标识</th><th>名称</th><th>描述</th><th>权限</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-if="list.length===0"><td colspan="5" class="text-center text-muted">暂无角色</td></tr>
              <tr v-for="item in list" :key="item.roleId">
                <td><code>{{ item.roleId }}</code></td><td>{{ item.name }}</td>
                <td class="text-muted small">{{ item.description||'-' }}</td>
                <td><span v-for="p in (item.permissions||[])" :key="p" class="badge badge-primary mr-1">{{ p }}</span><span v-if="!item.permissions||!item.permissions.length" class="text-muted">-</span></td>
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
      <!-- Modal -->
      <modal :show="showModal" @update:show="closeModal">
        <template #header><h5 class="modal-title">{{ editing ? '编辑角色' : '新建角色' }}</h5></template>
        <div>
          <div v-if="modalError" class="alert alert-danger">{{ modalError }}</div>
          <div class="form-group"><label class="form-control-label">角色标识</label><input v-model="form.roleId" type="number" class="form-control" placeholder="如：3" :disabled="!!editing" required /></div>
          <div class="form-group"><label class="form-control-label">名称</label><input v-model="form.name" class="form-control" placeholder="如：管理员" required /></div>
          <div class="form-group"><label class="form-control-label">描述</label><input v-model="form.description" class="form-control" placeholder="角色描述" /></div>
          <div class="form-group"><label class="form-control-label">权限列表</label><input v-model="permsStr" class="form-control" placeholder="权限ID，逗号分隔，如：1, 2" /></div>
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
import { roleAPI } from '@/api/rbac'
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
    const r = await roleAPI.list({ page: pg, pageSize: pageSize.value })
    list.value = r.roleList?.roleList || []
    totalCount.value = r.totalCount || 0
  }, '加载角色失败')
}

const { page, pageSize, totalCount, totalPages } = usePagination(fetchData)

// ── 删除 ──
async function del(item) {
  const ok = await notify.confirmDelete(`确定删除角色「${item.name}」吗？`)
  if (!ok) return
  try {
    await roleAPI.delete(item.roleId)
    await fetchData()
  } catch (e) {
    notify.error(userMsg(e, '删除'))
  }
}

// ── Modal 状态 ──
const { showModal, editing, modalLoading, modalError, openModal, closeModal } = useModal()
const form = ref({ roleId: '', name: '', description: '' })
const permsStr = ref('')

function openCreate() {
  form.value = { roleId: '', name: '', description: '' }
  permsStr.value = ''
  openModal()
}

function openEdit(item) {
  form.value = {
    roleId: item.roleId || '',
    name: item.name || '',
    description: item.description || ''
  }
  permsStr.value = (item.permissions || []).join(', ')
  openModal(item)
}

async function modalSubmit() {
  modalError.value = ''
  modalLoading.value = true

  const perms = permsStr.value.split(',').map(s => Number(s.trim())).filter(Boolean)
  const data = { ...form.value, roleId: Number(form.value.roleId), permissions: perms }

  if (!editing.value && !data.roleId) {
    modalError.value = '请输入角色标识'
    modalLoading.value = false
    return
  }

  try {
    if (editing.value) {
      await roleAPI.update(editing.value.roleId, data)
    } else {
      await roleAPI.create(data)
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
