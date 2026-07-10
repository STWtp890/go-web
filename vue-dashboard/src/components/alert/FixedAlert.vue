<template>
  <teleport to="body">
    <div v-if="confirms.length" class="fixed-confirm-backdrop" @click.self="cancelLatest">
      <div class="fixed-confirm-card card">
        <div class="card-body text-center py-4">
          <i :class="icon" :style="{ color: iconColor, fontSize: '36px' }" />
          <h5 class="mt-3">{{ latest.title }}</h5>
          <p class="text-muted small" v-html="latest.msg" />
          <div class="d-flex justify-content-center gap-2 mt-3">
            <button class="btn btn-outline-secondary btn-sm" @click="cancelLatest">取消</button>
            <button class="btn btn-sm" :class="confirmBtnClass" @click="confirmLatest">{{ confirmText }}</button>
          </div>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup>
import { computed, onBeforeUnmount } from 'vue'
import { useNotify } from '@/composables/useNotify'
const { confirms, remove, dismissAll } = useNotify()

// 组件卸载时清理所有未处理的 Promise
onBeforeUnmount(() => dismissAll())

const latest = computed(() => confirms.value.length ? confirms.value[confirms.value.length - 1] : null)

const icon = computed(() => {
  const t = latest.value?.type
  if (t === 'danger') return 'icon-attention'
  if (t === 'warning') return 'icon-attention'
  return 'icon-info-circled'
})

const iconColor = computed(() => {
  const t = latest.value?.type
  if (t === 'danger') return '#e74c3c'
  if (t === 'warning') return '#f59e0b'
  return '#3b82f6'
})

const confirmText = computed(() => {
  if (!latest.value) return '确定'
  return latest.value.actions?.[0]?.text || '确定'
})

const confirmBtnClass = computed(() => {
  if (!latest.value) return 'btn-primary'
  return latest.value.actions?.[0]?.class || 'btn-primary'
})

function confirmLatest() {
  if (!latest.value) return
  const act = latest.value.actions?.[0]
  remove(latest.value.id)
  if (act?.onClick) act.onClick()
}

function cancelLatest() {
  if (!latest.value) return
  const act = latest.value.actions?.[1]
  remove(latest.value.id)
  if (act?.onClick) act.onClick()
}
</script>

<style scoped>
.fixed-confirm-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3500;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, .35);
  backdrop-filter: blur(2px);
  -webkit-backdrop-filter: blur(2px);
}

.fixed-confirm-card {
  min-width: 340px;
  max-width: 460px;
  background: var(--app-bg-card);
  border: 1px solid var(--app-border);
  border-radius: 8px;
  color: var(--app-text-primary);
  box-shadow: 0 8px 32px rgba(0, 0, 0, .2);
  animation: fixedCardIn .25s ease;
}

@keyframes fixedCardIn {
  from { opacity: 0; transform: scale(.92); }
  to { opacity: 1; transform: scale(1); }
}
</style>
