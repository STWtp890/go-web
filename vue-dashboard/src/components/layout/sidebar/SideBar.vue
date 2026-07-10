<template>
  <aside class="app-sidebar sidebar" :class="{ open: show }">
    <!-- 移动端遮罩 -->
    <div class="sidebar-backdrop" @click="$emit('close')" />

    <div class="sidebar-logo">
      <a href="javascript:void(0)" class="logo-link">
        <i class="icon-star-filled logo-icon" />
        <span class="logo-text">{{ brandTitle }}</span>
      </a>
    </div>

    <nav class="sidebar-nav">
      <ul class="nav flex-column"><slot /></ul>
    </nav>

    <div v-if="$slots.footer" class="sidebar-footer">
      <slot name="footer" />
    </div>
  </aside>
</template>

<script setup>
defineProps({
  brandTitle: { type: String, default: '只是一个平台' },
  show: { type: Boolean, default: false }
})
defineEmits(['close'])
</script>

