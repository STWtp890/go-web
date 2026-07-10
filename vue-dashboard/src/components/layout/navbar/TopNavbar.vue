<template>
  <nav class="navbar navbar-expand-lg">
    <div class="container-fluid navbar-row">
      <!-- 左侧：汉堡按钮 -->
      <div class="navbar-left">
        <div class="navbar-toggle d-inline" :class="{ toggled: $sidebar.showSidebar }">
          <button type="button" class="navbar-toggler" @click="$sidebar.displaySidebar(!$sidebar.showSidebar)">
            <span class="navbar-toggler-bar bar1" />
            <span class="navbar-toggler-bar bar2" />
            <span class="navbar-toggler-bar bar3" />
          </button>
        </div>
      </div>

      <!-- 右侧：主题切换 -->
      <div class="navbar-right">
        <FloatingThemeToggle />
      </div>
    </div>
  </nav>
</template>

<script setup>
import { FloatingThemeToggle } from '@/components'
</script>

<style scoped>
.navbar-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: nowrap;
}

.navbar-left {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}

.navbar-right {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}
</style>
