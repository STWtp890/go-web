/*!
 * Vue White Dashboard v2.0 — Vue 3 + Vite + Pinia
 */
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import piniaPluginPersistedstate from 'pinia-plugin-persistedstate'
import App from './App.vue'
import router from './router'

import '@/assets/frontello/css/fontello.css'
import '@/assets/scss/dashboard.scss'

import GlobalDirectives from './globalDirectives'
import { SidebarPlugin } from '@/components/layout'

// 引入 API 层（激活 axios 拦截器）
import '@/api/index'

const pinia = createPinia()
pinia.use(piniaPluginPersistedstate)

const app = createApp(App)
app.use(pinia)
app.use(router)
app.use(GlobalDirectives)
app.use(SidebarPlugin)

// 初始化主题（store 创建时自动从 localStorage / 系统偏好读取）
import { useThemeStore } from '@/stores/theme'
const appTheme = useThemeStore()

// 深浅主题切换：动态加载 highlight.js 主题 CSS
import { watch } from 'vue'
const HLJS_LIGHT = 'https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.9.0/build/styles/github.min.css'
const HLJS_DARK  = 'https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.9.0/build/styles/github-dark.min.css'

function swapLink(id, href) {
  const old = document.getElementById(id)
  if (old) old.remove()
  if (!href) return
  const link = document.createElement('link')
  link.id = id
  link.rel = 'stylesheet'
  link.href = href
  document.head.appendChild(link)
}

watch(() => appTheme.isDark, (dark) => {
  swapLink('hljs-theme', dark ? HLJS_DARK : HLJS_LIGHT)
}, { immediate: true })

app.mount('#app')
