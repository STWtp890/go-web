<template>
  <div class="app-layout">
    <!-- 顶部导航栏 -->
    <top-navbar class="app-navbar" />

    <!-- 主区域: 侧边栏 + 内容 -->
    <div class="app-body">
      <side-bar
        brand-title="只是一个平台"
        :show="sidebarOpen"
        @close="sidebarOpen = false"
      >
        <sidebar-link
          v-for="item in visibleItems"
          :key="item.to"
          :to="item.to"
        >
          <i :class="item.icon" /><p>{{ item.label }}</p>
        </sidebar-link>

        <template #footer>
          <div class="sidebar-user">
            <template v-if="auth.isLoggedIn">
              <router-link to="/profile" class="sidebar-user-info">
                <i class="icon-user" />
                <div class="sidebar-user-text">
                  <span class="sidebar-user-name">你好! {{ auth.userInfo?.userName || '用户' }}</span>
                  <span class="sidebar-user-email">{{ auth.userInfo?.email || '' }}</span>
                </div>
              </router-link>
              <a class="sidebar-user-logout" @click.prevent="handleLogout">
                <i class="icon-power" />
                <span>退出登录</span>
              </a>
            </template>
            <template v-else>
              <router-link to="/login" class="sidebar-user-login">
                <i class="icon-lock" />
                <span>登录</span>
              </router-link>
            </template>
          </div>
        </template>
      </side-bar>

      <!-- 内容区 -->
      <main class="app-content" @click="closeSidebar">
        <router-view v-slot="{ Component }">
          <transition name="fade" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </main>
    </div>

    <!-- 底部页脚 -->
    <content-footer class="app-footer" />
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { sidebarState } from '@/components/layout'
import TopNavbar from './navbar/TopNavbar.vue'
import ContentFooter from './footer/ContentFooter.vue'
import SideBar from './sidebar/SideBar.vue'
import SidebarLink from './sidebar/SidebarLink.vue'

const router = useRouter()
const auth = useAuthStore()
const sidebarOpen = ref(false)

// ── 同步 $sidebar 全局状态（TopNavbar 汉堡按钮依赖） ──
watch(() => sidebarState.showSidebar, (v) => { sidebarOpen.value = v })
watch(sidebarOpen, (v) => { sidebarState.showSidebar = v })

// ── 侧边栏导航配置 ──
const navItems = [
  { to: '/dashboard',   icon: 'icon-chart-pie',    label: '仪表盘' },
  { to: '/notices',     icon: 'icon-volume-high',  label: '公告管理' },
  { to: '/articles',    icon: 'icon-doc-text',     label: '文章中心' },
  { to: '/reviews',     icon: 'icon-check',        label: '文章审核', requiresAuth: true, requiresAdmin: true },
  { to: '/roles',       icon: 'icon-tag',          label: '角色管理', requiresAuth: true, requiresRbac: true },
  { to: '/permissions', icon: 'icon-key',          label: '权限管理', requiresAuth: true, requiresRbac: true }
]

const visibleItems = computed(() =>
  navItems.filter(item =>
    (!item.requiresAuth || auth.isLoggedIn) &&
    (!item.requiresAdmin || auth.isAdmin) &&
    (!item.requiresRbac || auth.canManageRbac)
  )
)

function closeSidebar() {
  if (window.innerWidth < 992) sidebarOpen.value = false
}

function handleLogout() {
  auth.logout()
  router.push('/dashboard')
}
</script>
