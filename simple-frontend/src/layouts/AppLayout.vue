<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { BookOpen, Compass, FilePlus2, Home, LogOut, Menu, MessageCircle, Search, X } from '@lucide/vue'
import AppLogo from '@/components/AppLogo.vue'
import { useUserSessionStore } from '@/stores/session'
import { getInitials } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const session = useUserSessionStore()
const menuOpen = ref(false)

const navItems = [
  { name: 'overview', label: '概览', icon: Home },
  { name: 'mine', label: '我的文稿', icon: BookOpen },
  { name: 'explore', label: '公开广场', icon: Compass },
  { name: 'search', label: '搜索', icon: Search },
  { name: 'chat', label: '消息', icon: MessageCircle },
]

const pageTitle = computed(() => ({
  overview: '概览', mine: '我的文稿', explore: '公开广场', search: '搜索文稿', editor: '新建文稿',
  'markdown-detail': '阅读文稿', chat: '消息中心',
}[String(route.name)] ?? 'Paperplane'))

async function logout() {
  await session.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="app-shell">
    <div v-if="menuOpen" class="sidebar-scrim" @click="menuOpen = false" />
    <aside class="sidebar" :class="{ 'sidebar--open': menuOpen }">
      <div class="sidebar__header">
        <AppLogo />
        <button class="icon-button sidebar__close" type="button" aria-label="关闭菜单" @click="menuOpen = false"><X :size="20" /></button>
      </div>

      <RouterLink :to="{ name: 'editor' }" class="button button--primary button--full sidebar__create" @click="menuOpen = false">
        <FilePlus2 :size="18" />新建文稿
      </RouterLink>

      <nav class="sidebar__nav" aria-label="主导航">
        <RouterLink v-for="item in navItems" :key="item.name" :to="{ name: item.name }" class="sidebar__link" @click="menuOpen = false">
          <component :is="item.icon" :size="18" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="sidebar__notice">
        <span class="sidebar__notice-dot" />
        <div><strong>实时通道待接入</strong><span>HTTP 消息功能可用</span></div>
      </div>

      <div class="sidebar__profile">
        <span class="avatar">{{ getInitials(session.subject) }}</span>
        <div><strong>用户 #{{ session.subject || '—' }}</strong><span>普通用户</span></div>
        <button type="button" class="icon-button" aria-label="退出登录" @click="logout"><LogOut :size="17" /></button>
      </div>
    </aside>

    <main class="app-main">
      <header class="mobile-header">
        <button type="button" class="icon-button" aria-label="打开菜单" @click="menuOpen = true"><Menu :size="21" /></button>
        <strong>{{ pageTitle }}</strong>
        <RouterLink :to="{ name: 'editor' }" class="icon-button" aria-label="新建文稿"><FilePlus2 :size="20" /></RouterLink>
      </header>
      <RouterView v-slot="{ Component }">
        <Transition name="page" mode="out-in"><component :is="Component" /></Transition>
      </RouterView>
    </main>
  </div>
</template>
