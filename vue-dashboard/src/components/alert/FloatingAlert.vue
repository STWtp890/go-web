<template>
  <teleport to="body">
    <div class="float-alerts">
      <transition-group name="float-alert">
        <div
          v-for="a in alerts"
          :key="a.id"
          class="float-alert"
          :class="'float-' + a.type"
        >
          <i :class="iconClass(a.type)" class="float-alert-icon" />
          <span class="float-alert-msg">{{ a.msg }}</span>
          <button class="float-alert-close" @click="dismiss(a.id)">&times;</button>
        </div>
      </transition-group>
    </div>
  </teleport>
</template>

<script setup>
import { useFloatAlert } from '@/composables/useFloatAlert'
const { alerts, dismiss } = useFloatAlert()

function iconClass(type) {
  if (type === 'error') return 'icon-cancel-circled'
  if (type === 'success') return 'icon-ok-circled'
  if (type === 'info') return 'icon-info-circled'
  return 'icon-attention'
}
</script>

<style scoped>
.float-alerts {
  position: fixed;
  bottom: 20px;
  right: 20px;
  z-index: 4000;
  display: flex;
  flex-direction: column-reverse;
  gap: 8px;
  pointer-events: none;
  opacity: .8;
}

.float-alert {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border-radius: 6px;
  border: 1px solid;
  font-size: 14px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, .18);
  min-width: 280px;
  max-width: 400px;
  pointer-events: auto;
}

.float-warning {
  background: #fef3c7;
  border-color: #f59e0b;
  color: #d97706;
}

.float-error {
  background: var(--app-alert-danger-bg);
  border-color: var(--app-alert-danger-border);
  color: var(--app-alert-danger-text);
}

.float-success {
  background: var(--app-alert-success-bg);
  border-color: var(--app-alert-success-border);
  color: var(--app-alert-success-text);
}

.float-info {
  background: var(--app-alert-info-bg);
  border-color: var(--app-alert-info-border);
  color: var(--app-alert-info-text);
}

.float-alert-icon { font-size: 18px; flex-shrink: 0; }
.float-alert-msg { flex: 1; }

.float-alert-close {
  flex-shrink: 0;
  background: none;
  border: none;
  font-size: 20px;
  line-height: 1;
  cursor: pointer;
  color: inherit;
  opacity: .5;
  padding: 0 4px;
}
.float-alert-close:hover { opacity: 1; }

.float-alert-enter-active,
.float-alert-leave-active {
  transition: all .3s ease;
}
.float-alert-enter-from {
  opacity: 0;
  transform: translateX(40px);
}
.float-alert-leave-to {
  opacity: 0;
  transform: translateX(40px);
}
</style>
