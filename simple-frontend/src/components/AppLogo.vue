<script setup lang="ts">
import { Send } from '@lucide/vue'

interface Props {
  compact?: boolean
  inverted?: boolean
}

withDefaults(defineProps<Props>(), { compact: false, inverted: false })
</script>

<template>
  <span class="brand" :class="{ 'brand--inverted': inverted }">
    <span class="brand__mark"><Send :size="18" :stroke-width="2.2" /></span>
    <span v-if="!compact" class="brand__name">Paperplane</span>
  </span>
</template>
