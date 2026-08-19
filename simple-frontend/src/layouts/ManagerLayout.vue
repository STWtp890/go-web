<script setup lang="ts">
import { ClipboardCheck, LogOut, ShieldCheck } from '@lucide/vue'
import { useRouter } from 'vue-router'
import AppLogo from '@/components/AppLogo.vue'
import { useManagerSessionStore } from '@/stores/session'

const router = useRouter()
const session = useManagerSessionStore()

async function logout() {
  await session.logout()
  await router.replace({ name: 'manager-login' })
}
</script>

<template>
  <div class="manager-shell">
    <header class="manager-header">
      <AppLogo />
      <div class="manager-header__context"><ShieldCheck :size="17" /><span>管理控制台</span></div>
      <nav>
        <RouterLink :to="{ name: 'manager-requests' }"><ClipboardCheck :size="17" />申请审批</RouterLink>
      </nav>
      <div class="manager-header__session">
        <span>管理员 #{{ session.subject || '—' }}</span>
        <button type="button" class="button button--ghost button--small" @click="logout"><LogOut :size="16" />退出</button>
      </div>
    </header>
    <main class="manager-main"><RouterView /></main>
  </div>
</template>
