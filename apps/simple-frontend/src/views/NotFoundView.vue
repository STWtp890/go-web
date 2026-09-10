<script setup lang="ts">
import { ArrowLeft, FileQuestion } from '@lucide/vue'
</script>

<template>
  <main class="not-found">
    <FileQuestion :size="46" />
    <span>404</span>
    <h1>这一页飞走了</h1>
    <p>你访问的页面不存在，或已经被移动到别处。</p>
    <RouterLink :to="{ name: 'landing' }" class="button button--dark"><ArrowLeft :size="17" />返回首页</RouterLink>
  </main>
</template>
