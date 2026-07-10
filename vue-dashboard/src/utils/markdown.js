import { marked } from 'marked'
import { markedHighlight } from 'marked-highlight'
import hljs from 'highlight.js'
import katex from 'katex'

// ── marked 全局配置 ──
marked.use(markedHighlight({
  langPrefix: 'hljs language-',
  highlight(code, lang) {
    if (lang && hljs.getLanguage(lang)) {
      return hljs.highlight(code, { language: lang, ignoreIllegals: true }).value
    }
    return hljs.highlightAuto(code).value
  }
}))

marked.setOptions({
  breaks: true,
  gfm: true
})

/**
 * 渲染 Markdown 内容为 HTML，内嵌 LaTeX 公式（KaTeX）。
 * 支持：
 *   - 行内公式: $E = mc^2$
 *   - 块级公式: $$\int_0^\infty e^{-x^2} dx$$
 */
export function renderMarkdown(content) {
  if (!content) return ''

  const blocks = []

  // 1. 保护块级公式 $$...$$
  let processed = content.replace(/\$\$([\s\S]+?)\$\$/g, (_, tex) => {
    const idx = blocks.length
    blocks.push({ tex: tex.trim(), display: true })
    return `\x00LATEX_BLOCK_${idx}\x00`
  })

  // 2. 保护行内公式 $...$（排除 $$）
  processed = processed.replace(/(?<!\$)\$(?!\$)([^$\n]+?)\$(?!\$)/g, (_, tex) => {
    const idx = blocks.length
    blocks.push({ tex: tex.trim(), display: false })
    return `\x00LATEX_INLINE_${idx}\x00`
  })

  // 3. Markdown → HTML
  let html = marked.parse(processed)

  // 4. 占位符 → KaTeX HTML
  blocks.forEach((b, idx) => {
    const rendered = katex.renderToString(b.tex, {
      displayMode: b.display,
      throwOnError: false,
      output: 'html'
    })
    html = html.replace(`\x00LATEX_BLOCK_${idx}\x00`, rendered)
    html = html.replace(`\x00LATEX_INLINE_${idx}\x00`, rendered)
  })

  // 5. 为标题添加 id，支持锚点跳转（TOC 链接如 #1-前言）
  html = html.replace(/<(h[1-6])>(.*?)<\/\1>/gi, (match, tag, inner) => {
    const id = headingSlug(inner)
    return id ? `<${tag} id="${id}">${inner}</${tag}>` : match
  })

  return html
}

/** 从标题 HTML 内容生成锚点 ID */
function headingSlug(html) {
  // 去除内嵌标签（<strong>、<code>、<a> 等）
  const text = html.replace(/<[^>]*>/g, '').trim()
  if (!text) return ''
  return text
    .replace(/\s+/g, '-')           // 空格 → -
    .replace(/[^\w\u4e00-\u9fff.-]/g, '')  // 保留字母/数字/中文/点/短横
    .replace(/\.-/g, '-')           // "1.-前言" → "1-前言"
    .replace(/-{2,}/g, '-')         // 合并连续短横
    .replace(/[-.]+$/, '')          // 去除尾部符号
}
