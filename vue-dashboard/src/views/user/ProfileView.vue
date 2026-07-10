<template>
  <div>
    <div class="row"><div class="col-lg-8">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-user text-primary mr-2" />个人中心</h3>
      </div>
      <div class="card">
        <div class="card-header"><h5 class="card-title mb-0">用户信息</h5></div>
        <div class="card-body">
          <div v-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
          <div v-if="loading" class="text-center py-4"><div class="spinner-border text-primary" /></div>
          <template v-else-if="profile">
            <table class="table table-borderless"><tbody>
              <tr><td class="font-weight-bold" style="width:120px">用户 ID</td><td><code>{{ profile.userId }}</code></td></tr>
              <tr><td class="font-weight-bold">用户名</td><td>{{ profile.userName }}</td></tr>
              <tr><td class="font-weight-bold">邮箱</td><td>{{ profile.email }}</td></tr>
            </tbody></table>
          </template>
          <div v-else class="alert alert-warning">无法加载用户信息，请重新登录</div>
          <hr />
          <button class="btn btn-danger btn-sm" @click="handleLogout"><i class="icon-power" /> 退出登录</button>
        </div>
      </div>
    </div></div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { userAPI } from '@/api/user'

const router = useRouter()
const auth = useAuthStore()
const profile = ref(null)
const loading = ref(false)
const errorMsg = ref('')

const userId = auth.userInfo && auth.userInfo.userId
if (!userId) { router.push('/login') }
else {
  loading.value = true
  try { const r = await userAPI.getProfile(userId); profile.value = r.userProfile }
  catch (e) { errorMsg.value = '加载用户信息失败：' + ((e.response && e.response.data && e.response.data.message) || '网络错误') }
  finally { loading.value = false }
}

async function handleLogout() {
  try { await userAPI.logout(auth.userInfo.userId) } catch (e) { /* 即使后端登出失败也清除本地状态 */ }
  auth.logout(); router.push('/dashboard')
}
</script>
