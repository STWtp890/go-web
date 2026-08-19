import { ref } from 'vue'
import { defineStore } from 'pinia'

export interface Toast {
  id: number
  title: string
  message?: string
  tone: 'success' | 'error' | 'info'
}

export const useToastStore = defineStore('toast', () => {
  const toasts = ref<Toast[]>([])
  let nextId = 1

  function show(toast: Omit<Toast, 'id'>) {
    const id = nextId++
    toasts.value.push({ id, ...toast })
    window.setTimeout(() => dismiss(id), 4200)
  }

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((toast) => toast.id !== id)
  }

  return { toasts, show, dismiss }
})
