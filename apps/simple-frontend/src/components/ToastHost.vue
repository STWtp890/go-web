<script setup lang="ts">
import { CircleCheck, CircleX, Info, X } from '@lucide/vue'
import { useToastStore } from '@/stores/toast'

const toastStore = useToastStore()
</script>

<template>
  <Teleport to="body">
    <div class="toast-host" aria-live="polite">
      <TransitionGroup name="toast">
        <article v-for="toast in toastStore.toasts" :key="toast.id" class="toast" :class="`toast--${toast.tone}`">
          <CircleCheck v-if="toast.tone === 'success'" :size="20" />
          <CircleX v-else-if="toast.tone === 'error'" :size="20" />
          <Info v-else :size="20" />
          <div class="toast__content">
            <strong>{{ toast.title }}</strong>
            <span v-if="toast.message">{{ toast.message }}</span>
          </div>
          <button type="button" class="icon-button" aria-label="关闭提示" @click="toastStore.dismiss(toast.id)"><X :size="16" /></button>
        </article>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
