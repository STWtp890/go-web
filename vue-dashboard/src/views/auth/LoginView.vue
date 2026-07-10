<template>
  <div class="auth-page">
    <!-- 返回按钮 -->
    <router-link to="/dashboard" class="auth-back-btn">
      <i class="icon-left-open" /> 返回首页
    </router-link>

    <div class="row justify-content-center w-100">
      <div class="col-lg-4 col-md-6">
        <div class="card">
          <div class="card-header"><h5 class="card-title mb-0"><i class="icon-lock" /> 登录</h5></div>
          <div class="card-body">

            <form @submit.prevent="handleLogin">
              <div class="form-group"><label class="form-control-label">邮箱 / 用户ID</label><input v-model="form.userEmail" class="form-control" placeholder="请输入邮箱或用户ID" required /></div>
              <div class="form-group"><label class="form-control-label">密码</label><input v-model="form.userPassword" type="password" class="form-control" placeholder="请输入密码" required /></div>
              <button class="btn btn-primary btn-block btn-lg" :disabled="loading" type="submit">{{ loading ? '登录中...' : '登 录' }}</button>
            </form>
            <div class="text-center mt-3"><router-link to="/register" class="text-primary">还没有账号？立即注册</router-link></div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { authAPI } from '@/api/auth'
import { floatAlert } from '@/composables/useFloatAlert'

const router = useRouter()
const auth = useAuthStore()
const form = reactive({ userEmail: '', userPassword: '' })
const loading = ref(false)

async function handleLogin() {
  loading.value = true
  try {
    const res = await authAPI.login({ userEmail: form.userEmail, userPassword: form.userPassword })
    auth.setTokens(res.accessToken, res.refreshToken, res.expiresIn)
    auth.setUserInfo(res.userInfo)
    router.push('/dashboard')
  } catch (e) {
    floatAlert.warning('邮箱或密码错误，请检查后重试')
  } finally { loading.value = false }
}
</script>
