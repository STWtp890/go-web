<script setup lang="ts">
import { FileText } from '@lucide/vue'

interface Props {
  title: string
  description: string
}

defineProps<Props>()
</script>

<template>
  <div class="empty-state">
    <span class="empty-state__icon"><FileText :size="26" /></span>
    <h3>{{ title }}</h3>
    <p>{{ description }}</p>
    <slot />
  </div>
</template>
