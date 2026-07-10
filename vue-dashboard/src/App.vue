<template>
  <div id="app">
    <router-view :key="$route.fullPath" />
    <div
      v-show="showSidebar"
      id="bodyClick"
      @click="closeSidebar"
    />
    <FixedAlert />
    <FloatingAlert />
  </div>
</template>

<script setup>
import { watch, computed } from 'vue'
import { useRoute } from 'vue-router'
import { sidebarState } from '@/components/layout'
import { FixedAlert, FloatingAlert } from '@/components'

const route = useRoute()

const showSidebar = computed(() => sidebarState.showSidebar)

// 移动端：sidebar 展开/收起时切换 nav-open 类
watch(() => sidebarState.showSidebar, (val) => {
  document.documentElement.classList.toggle('nav-open', val)
})

function closeSidebar() {
  sidebarState.showSidebar = false
}
</script>

<style lang="scss">
html, body {
  height: 100%;
  overflow: hidden;
}

#app {
  height: 100%;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  text-align: center;
  color: var(--app-text-primary);
}

/* 修复布局：wrapper 裁剪溢出，内容区内部滚动 */
.wrapper {
  overflow: hidden !important;
}

.main-panel > .content {
  overflow-y: auto;
  overflow-x: hidden;
  height: calc(100vh - 70px);  /* 减去 footer + navbar 高度 */
  padding-bottom: 20px !important;
}

/* 认证页专用：全屏居中（背景由 *-mode.scss 管理） */
.auth-page {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  position: relative;
}

/* 认证页返回按钮 */
.auth-back-btn {
  position: absolute;
  top: 24px;
  left: 24px;
  color: var(--app-text-secondary);
  font-size: 14px;
  text-decoration: none;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  transition: color 0.2s;

  &:hover {
    color: var(--app-brand);
    text-decoration: none;
  }
}
</style>
