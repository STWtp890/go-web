import { ref, watch } from 'vue'
import gmLight      from 'github-markdown-css/github-markdown.css?url'
import gmDark       from 'github-markdown-css/github-markdown-dark.css?url'
import gmDarkDimmed from 'github-markdown-css/github-markdown-dark-dimmed.css?url'

const THEME_LINK_ID = 'gm-theme'

// ── 浅色 CSS 变量 ──
const LIGHT_VARS = {
  '--bgColor-default':    '#ffffff',
  '--fgColor-default':    '#1f2328',
  '--bgColor-muted':      '#f6f8fa',
  '--fgColor-muted':      '#656d76',
  '--fgColor-accent':     '#0969da',
  '--borderColor-default':'#d0d7de',
  '--borderColor-muted':  '#d8dee4',
}

// ── 主题注册表 ──
//   `url`:  CSS 文件 URL（深色自带结构+变量；浅色仅结构，变量由 vars 提供）
//   `vars`: 浅色额外 CSS 变量
const themes = {
  'github-light': {
    label: 'GitHub Light',
    url:  gmLight,
    vars: LIGHT_VARS,
  },
  'github-dark': {
    label: 'GitHub Dark',
    url:  gmDark,
  },
  'github-dark-dimmed': {
    label: 'GitHub Dark Dimmed',
    url:  gmDarkDimmed,
  },
}

const current = ref(localStorage.getItem('md-theme') || 'github-dark')

export function useMarkdownTheme() {
  function set(name) {
    if (themes[name]) current.value = name
  }

  return { current, themes, set }
}

function apply(name) {
  const t = themes[name]
  if (!t) return

  // 1. 替换 CSS 文件
  const old = document.getElementById(THEME_LINK_ID)
  if (old) old.remove()

  const link = document.createElement('link')
  link.id = THEME_LINK_ID
  link.rel = 'stylesheet'
  link.href = t.url
  document.head.appendChild(link)

  // 2. CSS 变量（浅色额外注入，深色由 CSS 文件自身提供）
  setThemeVars(t.vars || null)
}

const VAR_STYLE_ID = 'gm-vars'
function setThemeVars(vars) {
  let el = document.getElementById(VAR_STYLE_ID)
  if (!el) {
    el = document.createElement('style')
    el.id = VAR_STYLE_ID
  }
  if (!vars) { el.textContent = ''; return }
  const rules = Object.entries(vars)
    .map(([k, v]) => `${k}: ${v};`)
    .join(' ')
  el.textContent = `.markdown-body { ${rules} }`
  // 确保在 CSS link 之后注入
  if (el.parentNode) el.parentNode.removeChild(el)
  document.head.appendChild(el)
}

// ── 模块级初始化：加载 CSS + 监听变更（必须在所有定义之后）──
apply(current.value)
watch(current, (val) => {
  localStorage.setItem('md-theme', val)
  apply(val)
})
