<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ArrowRight, BadgeCheck, ShieldCheck } from '@lucide/vue'
import AppLogo from '@/components/AppLogo.vue'
import { managerApi } from '@/api/manager'
import { getApiError } from '@/api/client'
import { useManagerSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

const route = useRoute()
const router = useRouter()
const session = useManagerSessionStore()
const toast = useToastStore()
const isApply = computed(() => route.name === 'manager-apply')

const username = ref('')
const password = ref('')
const email = ref('')
const reason = ref('')
const loading = ref(false)
const errorMessage = ref('')

async function submit() {
  errorMessage.value = ''
  loading.value = true
  try {
    if (isApply.value) {
      const result = await managerApi.register({ username: username.value.trim(), password: password.value, email: email.value.trim() || undefined, reason: reason.value.trim() || undefined })
      toast.show({ tone: 'success', title: `申请 #${result.data.requestId} 已提交`, message: '审批通过后即可使用管理员账号登录' })
      await router.replace({ name: 'manager-login' })
      return
    }
    await managerApi.login({ username: username.value.trim(), password: password.value })
    session.setSession()
    toast.show({ tone: 'success', title: '管理员身份验证成功' })
    await router.replace(typeof route.query.redirect === 'string' ? route.query.redirect : '/manager/requests')
  } catch (error) {
    errorMessage.value = getApiError(error).message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="manager-access">
    <header><RouterLink :to="{ name: 'landing' }"><AppLogo /></RouterLink><RouterLink :to="{ name: 'landing' }"><ArrowLeft :size="16" />返回用户站</RouterLink></header>
    <main class="manager-access__card">
      <div class="manager-access__icon"><ShieldCheck :size="27" /></div>
      <span class="manager-access__kicker">PAPERPLANE ADMINISTRATION</span>
      <h1>{{ isApply ? '申请管理权限' : '管理控制台' }}</h1>
      <p>{{ isApply ? '提交资料后，需要由现有管理员完成审批。' : '请使用已审批并处于启用状态的管理员账号。' }}</p>

      <form @submit.prevent="submit">
        <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
        <label class="field"><span>用户名</span><input v-model.trim="username" minlength="3" maxlength="64" autocomplete="username" placeholder="admin_name" required /></label>
        <label v-if="isApply" class="field"><span>联系邮箱 <small>可选</small></span><input v-model.trim="email" type="email" autocomplete="email" placeholder="name@example.com" /></label>
        <label class="field"><span>密码</span><input v-model="password" type="password" minlength="6" maxlength="128" :autocomplete="isApply ? 'new-password' : 'current-password'" placeholder="至少 6 个字符" required /></label>
        <label v-if="isApply" class="field"><span>申请理由 <small>可选</small></span><textarea v-model.trim="reason" maxlength="512" rows="4" placeholder="简要说明你需要管理权限的原因" /></label>
        <button type="submit" class="button button--dark button--large button--full" :disabled="loading">
          <BadgeCheck v-if="isApply" :size="18" />{{ loading ? '正在提交…' : isApply ? '提交申请' : '验证并进入' }}<ArrowRight v-if="!isApply && !loading" :size="18" />
        </button>
      </form>
      <p class="manager-access__switch">{{ isApply ? '申请已经获批？' : '还没有管理员账号？' }} <RouterLink :to="{ name: isApply ? 'manager-login' : 'manager-apply' }">{{ isApply ? '返回登录' : '提交权限申请' }}</RouterLink></p>
    </main>
  </div>
</template>
