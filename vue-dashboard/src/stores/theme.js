import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useThemeStore = defineStore('theme', () => {
  const isDark = ref(false)

  function apply() {
    document.documentElement.classList.toggle('dark-mode', isDark.value)
    localStorage.setItem('view-theme', isDark.value ? 'dark' : 'light')
  }

  function toggle() {
    isDark.value = !isDark.value
    apply()
  }

  // ── 自动初始化（首次创建 store 时执行）──
  const saved = localStorage.getItem('view-theme')
  if (saved === 'dark' || saved === 'light') {
    isDark.value = saved === 'dark'
  } else {
    isDark.value = window.matchMedia('(prefers-color-scheme: dark)').matches
  }
  apply()

  // 监听系统偏好变化（仅在用户未手动设置时）
  const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
  mediaQuery.addEventListener('change', (e) => {
    const s = localStorage.getItem('view-theme')
    if (!s) {
      isDark.value = e.matches
      apply()
    }
  })

  return { isDark, toggle }
})
