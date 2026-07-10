<template>
  <div class="auth-page">
    <router-link to="/dashboard" class="auth-back-btn">
      <i class="icon-left-open" /> 返回首页
    </router-link>

    <div class="row justify-content-center w-100">
      <div class="col-lg-4 col-md-6">
        <div class="card">
          <div class="card-header"><h5 class="card-title mb-0"><i class="icon-user" /> 注册</h5></div>
          <div class="card-body">
            <div v-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
            <div v-if="successMsg" class="alert alert-success">{{ successMsg }}</div>
            <form @submit.prevent="handleRegister">
              <div class="form-group"><label class="form-control-label">用户名</label><input v-model="form.userName" class="form-control" placeholder="请输入用户名" required /></div>
              <div class="form-group"><label class="form-control-label">邮箱</label><input v-model="form.email" type="email" class="form-control" placeholder="请输入邮箱" required /></div>
              <div class="form-group"><label class="form-control-label">密码</label><input v-model="form.userPassword" type="password" class="form-control" placeholder="请输入密码" required /></div>
              <button class="btn btn-primary btn-block btn-lg" :disabled="loading" type="submit">{{ loading ? '注册中...' : '注 册' }}</button>
            </form>
            <div class="text-center mt-3"><router-link to="/login" class="text-primary">已有账号？立即登录</router-link></div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { authAPI } from '@/api/auth'

const router = useRouter()
const form = reactive({ userName: '', email: '', userPassword: '' })
const loading = ref(false)
const errorMsg = ref('')
const successMsg = ref('')

async function handleRegister() {
  errorMsg.value = ''; successMsg.value = ''; loading.value = true
  try {
    await authAPI.register({ ...form })
    successMsg.value = '注册成功！即将跳转到登录页...'
    setTimeout(() => router.push('/login'), 1500)
  } catch (e) {
    errorMsg.value = (e.response && e.response.data && e.response.data.message) || '注册失败'
  } finally { loading.value = false }
}
</script>
