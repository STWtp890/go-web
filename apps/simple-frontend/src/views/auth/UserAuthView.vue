<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ArrowRight, Eye, EyeOff, Quote } from '@lucide/vue'
import AppLogo from '@/components/AppLogo.vue'
import { authApi } from '@/api/auth'
import { getApiError } from '@/api/client'
import { useUserSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

const route = useRoute()
const router = useRouter()
const session = useUserSessionStore()
const toast = useToastStore()

const isRegister = computed(() => route.name === 'register')
const email = ref('')
const nickname = ref('')
const password = ref('')
const showPassword = ref(false)
const loading = ref(false)
const errorMessage = ref('')

async function submit() {
  errorMessage.value = ''
  if (isRegister.value && nickname.value.trim().length < 3) {
    errorMessage.value = '昵称至少需要 3 个字符'
    return
  }
  if (password.value.length < 6) {
    errorMessage.value = '密码至少需要 6 个字符'
    return
  }
  loading.value = true
  try {
    if (isRegister.value) {
      await authApi.register({ email: email.value.trim(), password: password.value, nickname: nickname.value.trim() })
    }
    await authApi.login({ email: email.value.trim(), password: password.value })
    session.setSession()
    toast.show({ tone: 'success', title: isRegister.value ? '空间创建成功' : '欢迎回来', message: '你的创作空间已经准备好了' })
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/app'
    await router.replace(redirect)
  } catch (error) {
    errorMessage.value = getApiError(error).message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="auth-page">
    <section class="auth-page__aside">
      <RouterLink :to="{ name: 'landing' }"><AppLogo inverted /></RouterLink>
      <div class="auth-quote">
        <Quote :size="34" />
        <blockquote>“写作不是为了被看见，而是为了看见自己。”</blockquote>
        <span>— 一位持续记录的人</span>
      </div>
      <div class="auth-page__aside-foot">WRITE · THINK · SHARE</div>
    </section>

    <main class="auth-panel">
      <RouterLink :to="{ name: 'landing' }" class="auth-panel__back"><ArrowLeft :size="17" />返回首页</RouterLink>
      <form class="auth-form" @submit.prevent="submit">
        <div class="auth-form__heading">
          <span>{{ isRegister ? 'START YOUR JOURNEY' : 'WELCOME BACK' }}</span>
          <h1>{{ isRegister ? '创建你的空间' : '欢迎回来' }}</h1>
          <p>{{ isRegister ? '从一篇文稿开始，保存每一个值得的想法。' : '继续书写还没讲完的故事。' }}</p>
        </div>

        <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>

        <label v-if="isRegister" class="field">
          <span>昵称</span>
          <input v-model.trim="nickname" type="text" autocomplete="nickname" minlength="3" maxlength="32" placeholder="你希望我们如何称呼你" required />
        </label>
        <label class="field">
          <span>邮箱</span>
          <input v-model.trim="email" type="email" autocomplete="email" placeholder="name@example.com" required />
        </label>
        <label class="field">
          <span>密码</span>
          <span class="field__password">
            <input v-model="password" :type="showPassword ? 'text' : 'password'" :autocomplete="isRegister ? 'new-password' : 'current-password'" minlength="6" maxlength="128" placeholder="至少 6 个字符" required />
            <button type="button" class="icon-button" :aria-label="showPassword ? '隐藏密码' : '显示密码'" @click="showPassword = !showPassword">
              <EyeOff v-if="showPassword" :size="18" /><Eye v-else :size="18" />
            </button>
          </span>
        </label>

        <button class="button button--primary button--large button--full" type="submit" :disabled="loading">
          {{ loading ? '请稍候…' : isRegister ? '创建并进入空间' : '进入空间' }}<ArrowRight v-if="!loading" :size="18" />
        </button>
        <p class="auth-form__switch">
          {{ isRegister ? '已经有账号？' : '第一次来到这里？' }}
          <RouterLink :to="{ name: isRegister ? 'login' : 'register' }">{{ isRegister ? '直接登录' : '免费注册' }}</RouterLink>
        </p>
      </form>
    </main>
  </div>
</template>
