<template>
  <div class="md-theme-switch">
    <select :value="mdTheme" class="md-theme-select" @change="set($event.target.value)">
      <option v-for="(t, key) in themes" :key="key" :value="key">{{ t.label }}</option>
    </select>
  </div>
</template>

<script setup>
import { useMarkdownTheme } from '@/composables/useMarkdownTheme'

const { current: mdTheme, themes, set } = useMarkdownTheme()
</script>

<style scoped>
/* ── 统一 btn 风格：参照 ConfirmDialog .btn ── */
.md-theme-select {
  appearance: none;
  -webkit-appearance: none;
  width: auto;
  min-width: 154px;
  height: auto;
  padding: 5px 28px 5px 10px;
  font-size: .8125rem;
  font-weight: 400;
  line-height: 1.5;
  color: var(--app-text-primary);
  background-color: var(--app-bg-card);
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%23888' d='M6 8L1 3h10z'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 8px center;
  border: 1px solid var(--app-border);
  border-radius: 6px;
  box-shadow: none;
  cursor: pointer;
  transition: border-color .2s, box-shadow .2s;
}

.md-theme-select:hover {
  border-color: var(--app-brand);
  box-shadow: 0 0 0 1px rgba(45, 206, 137, .15);
}

.md-theme-select:focus {
  outline: none;
  border-color: var(--app-brand);
  box-shadow: 0 0 0 2px rgba(45, 206, 137, .2);
}

/* 下拉选项 — 跟随主题 */
.md-theme-select option {
  background: var(--app-bg-card);
  color: var(--app-text-primary);
}
</style>
