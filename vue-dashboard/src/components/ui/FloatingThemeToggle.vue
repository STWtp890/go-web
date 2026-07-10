<template>
  <button
    class="floating-theme-toggle"
    :title="theme.isDark ? '切换浅色模式' : '切换深色模式'"
    @click="theme.toggle()"
  >
    <!-- 太阳（深→浅）：橙黄暖色 -->
    <svg v-if="theme.isDark" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#f59e0b" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="12" r="5" fill="#fbbf24" stroke="none"/>
      <circle cx="12" cy="12" r="5"/>
      <line x1="12" y1="1" x2="12" y2="3"/>
      <line x1="12" y1="21" x2="12" y2="23"/>
      <line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/>
      <line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/>
      <line x1="1" y1="12" x2="3" y2="12"/>
      <line x1="21" y1="12" x2="23" y2="12"/>
      <line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/>
      <line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/>
    </svg>
    <!-- 月亮（浅→深）：灰蓝冷色 -->
    <svg v-else width="20" height="20" viewBox="0 0 24 24" fill="#94a3b8" stroke="#3b82f6" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>
    </svg>
  </button>
</template>

<script setup>
import { useThemeStore } from '@/stores/theme'
const theme = useThemeStore()
</script>

<style scoped>
.floating-theme-toggle {
  width: 36px;
  height: 36px;
  padding: 0;
  border-radius: 50%;
  border: 1px solid var(--app-border);
  background: var(--app-bg-card);
  color: var(--app-text-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  box-shadow: 0 2px 12px rgba(0, 0, 0, .12);
  transition: box-shadow .2s, background-color .3s, color .3s;
  outline: none;
  -webkit-tap-highlight-color: transparent;
}

.floating-theme-toggle:hover {
  box-shadow: 0 4px 20px rgba(0, 0, 0, .2);
  color: var(--app-brand);
}
</style>
