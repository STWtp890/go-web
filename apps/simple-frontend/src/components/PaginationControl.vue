<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'

interface Props {
  page: number
  totalPages: number
  total?: number
  disabled?: boolean
}

defineProps<Props>()
const emit = defineEmits<{ change: [page: number] }>()
</script>

<template>
  <nav v-if="totalPages > 1" class="pagination" aria-label="分页">
    <span v-if="total !== undefined" class="pagination__total">共 {{ total }} 项</span>
    <button type="button" class="pagination__button" :disabled="disabled || page <= 1" aria-label="上一页" @click="emit('change', page - 1)">
      <ChevronLeft :size="17" />
    </button>
    <span class="pagination__page"><strong>{{ page }}</strong> / {{ totalPages }}</span>
    <button type="button" class="pagination__button" :disabled="disabled || page >= totalPages" aria-label="下一页" @click="emit('change', page + 1)">
      <ChevronRight :size="17" />
    </button>
  </nav>
</template>
